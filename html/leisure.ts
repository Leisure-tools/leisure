export const VERSION = '/v1'
const DOC_CREATE = VERSION + '/doc/create/'
const DOC_GET = VERSION + '/doc/get/'
const DOC_LIST = VERSION + '/doc/list'
const SESSION_CLOSE = VERSION + '/session/close'
const SESSION_CONNECT = VERSION + '/session/connect/'
const SESSION_CREATE = VERSION + '/session/create/'
const SESSION_LIST = VERSION + '/session/list'
const SESSION_DOC = VERSION + '/session/document'
const SESSION_UPDATE = VERSION + '/session/update'
const SESSION_EDIT = VERSION + '/session/edit'
const SESSION_GET = VERSION + '/session/get'
const SESSION_SET = VERSION + '/session/set'

export interface TextReplacement {
  offset: number
  length: number
  text: string
}

export interface BlockReplacement {
  block: string
  text: string
}

export type Replacement = TextReplacement | BlockReplacement

export interface Edit {
  selectionOffset: number
  selectionLength: number
  replacements: Replacement[]
}

type UpdateGenerator = () => Edit
type UpdateHandler = (edit: Edit) => any
type ConnectHandler = (edit: Replacement[]) => any
type ErrOpts = { cause?: any }

class LeisureError extends Error {
  constructor(msg: string, options: ErrOpts = {}) {
    super(msg)
    Object.setPrototypeOf(this, LeisureError.prototype)
    if (options.cause) {
      ;(this as any).cause = options.cause
    }
  }
}

export class Leisure {
  server: string
  sessionName: string
  documentId: string
  updateGenerator: UpdateGenerator
  updateHandler: UpdateHandler
  errorHandler: (err: string) => any
  updating: false
  dead: false

  constructor(server: string, sessionName: string, documentId: string) {
    this.server = new URL(server).href
    this.sessionName = sessionName
    this.documentId = documentId
  }

  async delete() {
    return fetch(new URL(SESSION_CLOSE, this.server).href)
  }

  checkError(json: any) {
    if (json.error) {
      throw new LeisureError(json.error)
    }
    return json
  }

  async fetchJson(doingWhat: string, url: string) {
    return this.fetch(doingWhat, url, (r: Response) => r.json())
  }

  async fetch(
    doingWhat: string,
    url: string,
    filter: (r: Response) => Promise<any> = (r) => r.text(),
  ) {
    if (this.dead) {
      throw new LeisureError('not connected')
    }
    const response = await this.protect(doingWhat, () => fetch(url))
    return this.protect(`getting json while ${doingWhat}`, async () =>
      this.checkError(await filter(response)),
    )
  }

  async connect(org: boolean, handle: ConnectHandler) {
    const url = new URL(SESSION_CONNECT + this.sessionName, this.server)
    url.searchParams.set('doc', this.documentId)
    url.searchParams.set('org', JSON.stringify(!!org))
    let doc = await this.fetchJson('connecting', url.href)
    //console.log('GOT', doc)
    if (typeof doc == 'string') {
      doc = { document: doc }
    }
    const result = this.protect('handling edits from connect response', () =>
      handle(doc),
    )
    if (result instanceof Promise) {
      await result
    }
  }

  async update() {
    return this.fetchJson(
      'checking for updates',
      new URL(SESSION_UPDATE, this.server).href,
    )
  }

  async edit(e: Edit) {
    const url = new URL(SESSION_EDIT, this.server)
    const response = await this.protect('requesting edit', async () =>
      fetch(url.href, {
        method: 'POST',
        headers: {
          'content-type': 'application/json;charset=UTF-8',
        },
        body: JSON.stringify(e),
      }),
    )
    return this.protect(
      `getting json from edit response`,
      async () => this.checkError(await response.json()) as Edit,
    )
  }

  async get(name: string) {
    return this.fetchJson(
      `getting data "${name}"`,
      new URL(`${SESSION_GET}/${name}`, this.server).href,
    )
  }

  async set(name: string, value: any) {
    const url = new URL(`${SESSION_SET}/${name}`, this.server)
    const response = await this.protect(`setting data "${name}"`, async () =>
      fetch(url.href, {
        method: 'POST',
        headers: {
          'content-type': 'application/json;charset=UTF-8',
        },
        body: JSON.stringify(value),
      }),
    )
    return this.protect(
      `getting json from edit response`,
      async () => this.checkError(await response.json()) as Edit,
    )
  }

  async doEdits(edit: Edit) {
    try {
      let pendingEdits = await this.edit(edit)
      console.log('GOT EDITS', pendingEdits)
      let result = this.protect('handling update edit response', () =>
        this.updateHandler(pendingEdits),
      )
      if (result instanceof Promise) {
        result = await result
      }
      //update check changed blocks
      const blocks: String[] = []
      for (const repl of edit.replacements) {
        if ('block' in repl) {
          blocks.push(repl.block)
        }
      }
    } catch (err) {
      this.error(err)
    }
  }

  async updateLoop(
    generate: UpdateGenerator,
    handle: UpdateHandler,
    error: (err: string) => any,
  ) {
    this.updateHandler = handle
    this.errorHandler = error
    try {
      const hasUpdate = await this.update()
      if (hasUpdate) {
        // There is an update pending, process it
        const currentEdit = this.protect(
          'generating edits for update',
          generate,
        )
        await this.doEdits(currentEdit)
        //let pendingEdits = await this.edit(currentEdit)
        //console.log('GOT EDITS', pendingEdits)
        //const result = this.protect('handling update edit response', () =>
        //  handle(pendingEdits),
        //)
        //if (result instanceof Promise) {
        //  await result
        //}
      }
    } catch (err) {
      this.error(err)
      return
    }
    // wait for another update and handle it
    setTimeout(() => this.updateLoop(generate, handle, error))
  }

  protect<T>(doingWhat: string, code: () => T): T {
    try {
      return code()
    } catch (err) {
      throw this.error(err, `Error while ${doingWhat}`)
    }
  }

  error(err: any, ...items: any[]): LeisureError {
    console.log(this, 'error:', err, ...items)
    const opts: ErrOpts = {}
    if (!(err instanceof Error)) {
      opts.cause = err
    }
    return new LeisureError(`${this} error: ${items.join('')}`, opts)
  }
}
