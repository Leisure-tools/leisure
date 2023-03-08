import {VERSION} from './leisure.js'

const HEADLINE_RE = /^(\*+)( +)(.*)\n$/;
const HTML_RE = /^(#\+name:[^\n]*\n)?(.*)(#\+begin_html[^\n]*\n)(.*\n)(#\+end_html[^\n]*\n)/is;
const SOURCE_RE = /^(?:(#\+name: *)([^\n]*\n))?(.*)(#\+begin_src *([^ \n]*)[^\n]*\n)(.*\n)(#\+end_src[^\n]*\n)((.*\n)?(#\+result:.*\n))?$/is;
const MARKUP_RE = /\*[^*]+\*|\/[^/]+\//

export const ORG_PARSE = VERSION + "/org/parse";

interface Chunk {
  type: string
  id: string
  text: string
  next?: string
  prev?: string
  parent?: string
  children?: string[]
  raw?: any
  serial?: number
}

interface Headline extends Chunk {
  level: number
  levelStr?: string
  interStr?: string
  contentStr?: string
  hlClass?: string
}

interface Block extends Chunk {
  label: number
  labelEnd: number
  content: number
  end: number
  contentStr?: string
}

interface Source extends Block {
  options: string[]
  value?: any
  nameStart?: number
  nameEnd?: number
  srcStart?: number
  name?: string
  language?: string
  inter1Str?: string
  beginStr?: string
  contentStr?: string
  endStr?: string
  inter2Str?: string
  resultStr?: string
}

interface Table extends Chunk {
  cells: string[][] // 2D array of cell strings
  values: any[][]   // 2D array of JSON-compatible values
  // these are relevant only if there is a preceding name element
  nameStart: number
  nameEnd: number   // this is 0 if there is no name
  tblStart: number  // this is 0 if there is no name
}

const templateNames = new Set([
  'headline',
  'text',
  'source',
  'results',
  'html',
  'drawer',
  'keyword',
  'table',
])

type TemplateFunc = (opts: any)=> string;

const compile = (window as any).Handlebars.compile as (template: string)=> TemplateFunc;

export async function parseOrg(url: string) {
  const text = await (await fetch(url)).text()
  const result = await (await fetch(new URL(ORG_PARSE, document.location.href), {
    method: 'POST',
    body: text,
  })).json()
  return result
}

function isSource(ch: Chunk): ch is Source {
  return ch.type === 'source'
}

export function renderText(text: string) {
  let pos = 0
  let result = ''

  while (text.length) {
    const mark = text.match(MARKUP_RE)
    if (!mark) {
      result += text.slice(pos)
      break
    }
    result += text.slice(pos, mark.index)
    const matched = mark[0]
    switch (matched[0]) {
      case '*':
        result += `<b>${renderText(matched.slice(1, matched.length - 1))}</b>`
        break
      case '/':
        result += `<i>${renderText(matched.slice(1, matched.length - 1))}</i>`
        break
    }
    text = text.slice(mark.index + matched.length)
  }
  return result
}

export class OrgRenderer {
  templates: {[t: string]: TemplateFunc}
  chunks: {[id: string]: Chunk}
  serial: number
  orphans: HTMLDivElement

  constructor(templateChunks: Chunk[]) {
    const scripts = [] as HTMLScriptElement[]
    const templates = [] as Source[]
    this.templates = {}
    this.chunks = {}
    this.serial = 0
    this.orphans = document.createElement('div')
    this.orphans.style.display = 'none'
    document.body.append(this.orphans)
    for (const chunk of templateChunks) {
      this.populateChunk(chunk)
      if (isSource(chunk)) {
        if (chunk.language === 'html' && templateNames.has(chunk.name)) {
          templates.push(chunk)
        } else if (chunk.language === 'css') {
          const style = document.createElement('style')
          style.setAttribute('org-id', chunk.id)
          style.textContent = chunk.contentStr
          document.body.append(style)
        } else if (chunk.language === 'javascript' || chunk.language === 'js') {
          const script = document.createElement('script') as HTMLScriptElement
          script.lang = 'javascript'
          script.setAttribute('org-id', chunk.id)
          script.textContent = chunk.contentStr
          script.type = 'module'
          scripts.push(script)
        }
      }
    }
    // run scripts first so they can register helpers, partials, etc.
    for (const script of scripts) {
      document.body.append(script)
    }
    for (const template of templates) {
      console.log('ADDING TEMPLATE', template)
      this.templates[template.name] = compile(template.contentStr)
    }
  }

  connect(result: any) {
    for (const chunk of result.chunks) {
      this.chunks[chunk.id] = chunk
    }
    for (const chunk of result.chunks) {
      this.displayChunk(chunk)
    }
    this.clearOrphans()
  }

  clearOrphans() {
    this.orphans.innerHTML = ''
  }

  update(changes: any) {
    this.serial++
    const all = [] as Chunk[]
    const changed = new Set() as Set<string>;
    for (const removed of changes.removed ?? []) {
      const dom = this.domFor(removed)
      dom && dom.remove()
    }
    for (const list of [changes.added ?? [], changes.changed ?? []]) {
      for (const chunk of list) {
        this.chunks[chunk.id] = chunk
        all.push(chunk)
        changed.add(chunk.id)
      }
    }
    for (const id of Object.keys(changes.linked ?? {})) {
      const chunk = this.chunks[id]

      if (chunk) {
        for (const link of Object.keys(changes.linked[id])) {
          const value = changes.linked[id][link]
          if (value) {
            chunk[link] = value
          } else {
            delete chunk[link]
          }
        }
        if (!changed.has(id)) {
          // chunk has not changed in other ways so it is safe to smash serial
          chunk.serial = this.serial
          all.push(chunk)
        }
      }
    }
    this.orderChunks(changes.order, all)
    for (const chunk of all) {
      this.populateChunk(chunk)
    }
    for (const chunk of all) {
      this.displayChunk(chunk)
    }
    this.clearOrphans()
    console.log('updated', all)
  }

  orderChunks(chunkOrder: string[], chunks: Chunk[]) {
    const order = {} as {[id: string]: number}
    let pos = 0
    for (const id of chunkOrder) {
      order[id] = pos++
    }
    chunks.sort((a, b)=> order[a.id] - order[b.id])
  }

  domFor(chunk: Chunk | string) {
    if (typeof chunk != 'string') {
      chunk = chunk.id
    }
    return document.querySelector(`[x-leisure-orgid="${chunk}"]`) as HTMLElement
  }

  placeDom(dom: HTMLElement, chunk: Chunk) {
    let prev = chunk.prev
    if (prev != chunk.parent) {
      while (prev) {
        const prevChunk = this.chunks[prev]
        if (prevChunk.parent === chunk.parent) {
          break
        }
        prev = prevChunk.parent
      }
    }
    const prevDom = prev && this.domFor(prev)
    const parentDom = chunk.parent && this.domFor(chunk.parent)
    const currentParentDom = chunk.parent && dom.parentElement?.closest('[x-leisure-orgId]')
    if (prevDom && (prevDom.nextElementSibling == dom || prevDom == currentParentDom)) {
      return
    }
    if (chunk.parent && chunk.parent == prev) {
      const parentContent = parentDom && parentDom.querySelector('[x-leisure-headline-content]')
      if (parentContent) {
        parentContent.insertBefore(dom, parentContent.firstChild)
        return
      }
    } else if (prev && prevDom) {
      prevDom.after(dom)
    } else {
      document.body.insertBefore(dom, document.body.firstChild)
    }
  }

  createDomFor(chunk: Chunk) {
    const dom = document.createElement('div')
    dom.setAttribute('x-leisure-orgid', chunk.id)
    return dom
  }

  displayChunk(chunk: Chunk) {
    const dom = this.domFor(chunk) || this.createDomFor(chunk)
    this.placeDom(dom, chunk)
    if (dom && dom.getAttribute('x-leisure-type') === 'headline' && chunk.type != 'headline') {
      const oldContents = dom.querySelector('[x-leisure-headline-content]')
      if (oldContents) {
        this.orphans.append(oldContents)
      }
    }
    dom.setAttribute('x-leisure-type', chunk.type)
    this.populateChunk(chunk)
    const children = chunk.type === 'headline' && dom.getAttribute('x-leisure-type') === 'headline' &&
      dom.querySelector('[x-leisure-headline-content]')
    children && children.remove()
    dom.innerHTML = this.renderChunk(chunk)
    if (children) {
      while (children.firstChild) {
        dom.querySelector('[x-leisure-headline-content]').append(children.firstChild)
      }
    }
  }

  // add properties to chunk to support templates
  populateChunk(chunk: Chunk) {
    if (chunk.serial === this.serial) {
      return
    }
    chunk.serial = this.serial
    this.chunks[chunk.id] = chunk
    switch (chunk.type) {
	  case 'headline': {
        const hl = chunk as Headline
        const [, level, inter, content] = chunk.text.match(HEADLINE_RE)
        hl.raw = {level, inter, content}
        hl.contentStr = content
        hl.hlClass = hl.level < 5 ? `leisure-hl-${hl.level}` : 'leisure-hl-deep'
        break
      }
	  case 'source': {
        const src = chunk as Source
        const [, preName, name, inter1, begin, language, content, end, inter2, result] = chunk.text.match(SOURCE_RE)
        src.raw = {preName, name, inter1, begin, language, content, end, inter2, result}
        src.name = (name ?? '').trim()
        src.language = (language ?? '').toLowerCase()
        src.contentStr = content
        src.resultStr = result
      }
      case 'block': {
        const bl = chunk as Block
        const text = bl.text
        bl.raw = {start: text.slice(0, bl.content), content: text.slice(bl.content, bl.end),
                  end: text.slice(bl.end)}
        bl.contentStr = bl.raw.content
        break
      }
	  case 'drawer':
        break
	  default:
        break
    }
  }

  renderChunk(chunk: Chunk) {
    const template = this.templates[chunk.type]
    if (template) {
      return template(chunk)
    }
    return renderText(chunk.text)
  }
}
