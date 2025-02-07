import { VERSION, Leisure } from "./leisure.js"

const WINDOW = window as any
const structuredClone = WINDOW.structuredClone
const HEADLINE_RE = /^(\*+)( +)(.*)\n$/
const SOURCE_RE =
  /^(?:(#\+name: *)([^\n]*\n))?(.*)(#\+begin_src *([^ \n]*)[^\n]*\n)(.*)(#\+end_src[^\n]*\n)((.*)(#\+result:.*\n))?$/is
const DRAWER_RE = /^([^\n]*)( *\n)(.*\n)?(:end:)( *\n)$/
const MARKUP_RE = /\[\[([^\]]*)\](?:\[([^\]]*)\])?\]|\*[^*]+\*|\/[^/]+\/|\n\n/
const VIEW_RE =
  /#\+begin_src +html (?:[^\n]+ )?:view +([^\n \/]+)(?:\/([^\n ]+))?(?: [^\n]+)?\n/is
const LEISURE_LINK_RE = /^leisure:(.*)$/i
const HREF_LINK_RE = /^http(s)?:.*$/i
const KEYWORD_RE = /^(#\+)([^ \n]+)(: *)([^ \n]*)( *\n)$/i
const Prism = WINDOW.Prism as any
const LEISURE_PATH = /^@?([-\w])((?:\.[\[\]\w])*)/

/**
 * list of functions that return true if a node is a text field
 */
export const TEXT_FIELD_MATCHERS = [(node) => node instanceof HTMLInputElement]

export const ORG_PARSE = VERSION + "/org/parse"

function classProps(cls: any) {
  return Object.getOwnPropertyNames(cls.prototype)
}

if (!Symbol.metadata) {
  (Symbol as any).metadata = Symbol("  __METADATA__  ")
}

function key<T>(value: T, ctx) {
  const {kind, metadata, name} = ctx
  if (kind === 'field') {
    if (!Object.getOwnPropertyDescriptor(metadata, "keys")) {
      metadata.keys = [...(metadata.keys ?? [])]
    }
    metadata.keys.push(name)
  }
}

function keys(cls: any) {
  return cls[Symbol.metadata].keys ?? []
}

export class Chunk {
  @key type: string
  @key id: string
  @key text: string
  @key next?: string
  @key prev?: string
  @key parent?: string
  @key children?: string[]
  @key raw?: any // the classified raw text -- when catenated it should be exactly the same as text
  @key serial?: number
  [key: string]: any // allow extra properties

  constructor(type: string, id: string) {
    this.type = type
    this.id = id
  }

  mainPopulate(renderer: OrgRenderer, chunk: any, index = true) {
    if (this.type && (this.type !== chunk.type || this.id !== chunk.id)) {
      // type changed, can't update this in-place
      console.log("TYPE OR ID CHANGED", this)
      renderer.chunkFor(chunk, index)
      return false
    }
    if (this.serial !== undefined && (this.serial === renderer.serial || this.serial === chunk.serial)) {
      return true
    }
    for (const key of keys(this.constructor)) {
      if (chunk[key] !== undefined) {
        this[key] = chunk[key]
      }
    }
    this.serial = renderer.serial
    if (index) {
      renderer.chunks[this.id] = this
    }
    this.raw = {}
    this.populate(renderer)
  }

  populate(_renderer: OrgRenderer) {}
}

export class Headline extends Chunk {
  @key level: number
  @key levelStr?: string
  @key interStr?: string
  @key contentStr?: string
  @key hlClass?: string
  @key hidden?: boolean

  async populate(renderer: OrgRenderer) {
    super.populate(renderer)
    ; [, this.levelStr, this.interStr, this.contentStr] = this.text.match(HEADLINE_RE)
    const raw = this.raw
    ; [raw.level, raw.inter, raw.content] = [this.levelStr, this.interStr, this.contentStr]
    this.hlClass = this.level < 5 ? `leisure-hl-${this.level}` : "leisure-hl-deep"
  }
}

export class Keyword extends Chunk {
  name: string
  value: string

  populate(renderer: OrgRenderer) {
    super.populate(renderer)
    const raw = this.raw
    ; [, raw.prec, this.name, raw.inter, this.value, raw.succ] = this.text.match(KEYWORD_RE)
    raw.name = this.name
    raw.value = this.value
    //if (this.name.toLowerCase() == "templates") {
    //  return this.addTemplates(await parseOrg(value))
    //}
  }
}

export class Block extends Chunk {
  @key label: number
  @key labelEnd: number
  @key content: number
  @key end: number
  @key contentStr?: string
  @key options: string[]
  @key blockType?: string

  populate(renderer: OrgRenderer) {
    super.populate(renderer)
    const text = this.text
    const raw = this.raw
    raw.start = text.slice(0, this.content),
    raw.content = text.slice(this.content, this.end),
    raw.end = text.slice(this.end),
    this.contentStr = raw.content
    if (this.type == "block") {
      this.blockType = text.slice(this.label, this.labelEnd)
    }
  }
}

export class Source extends Block {
  @key valueType?: string
  @key value?: any
  @key nameStart?: number
  @key nameEnd?: number
  @key srcStart?: number
  @key name?: string
  @key language?: string
  @key inter1Str?: string
  @key beginStr?: string
  @key endStr?: string
  @key inter2Str?: string
  @key resultStr?: string
  @key tags?: string[] // NOTE: fill in with prepare()

  populate(renderer: OrgRenderer) {
    super.populate(renderer)
    const match = this.text.match(SOURCE_RE) as any
    const raw = this.raw
    let language: string
    ; [ ,
        raw.preName,
        raw.name,
        raw.inter1,
        raw.begin,
        language,
        raw.content,
        raw.end,
        raw.inter2,
        raw.result,
        ] = match
    this.name = (raw.name ?? "").trim()
    this.language = (language ?? "").toLowerCase()
    this.contentStr = raw.content
    this.resultStr = raw.result
    if (this.name) {
      renderer.namedChunks[this.name] = this.id
    }
    const ref = refFor(this)
    if (ref) {
      renderer.refs[ref] = this.id
    }
    renderer.tag(this)
    this.valueType = optionValue(this, ":type")[0] || this.value?.LEISURE_TYPE
    renderer.scanView(this)
  }
}

export class Drawer extends Block {
  @key properties?: { [prop: string]: string }
  @key name?: string

  populate(renderer: OrgRenderer) {
    super.populate(renderer)
    const match = this.text.match(DRAWER_RE) as any
    const raw = this.raw
    ; [, raw.begin, raw.beginPad, raw.content, raw.end, raw.endPad] = match
    this.contentStr = raw.content
    this.name = raw.begin.slice(1, raw.begin.length - 1)
    if (this.name.toLowerCase() === "properties") {
      this.properties = {}
      for (const [prop, value] of Object.entries(this.properties)) {
        this.properties[prop.toLowerCase()] = String(value)
      }
      if (this.properties.hidden?.toLowerCase()?.trim() === "true") {
        const parent = renderer.chunks[this.parent] as Headline
        if (parent) {
          parent.hidden = true
        }
      }
    }
  }
}

export class Table extends Chunk {
  @key cells: string[][] // 2D array of cell strings
  @key values: any[][] // 2D array of JSON-compatible values
  // these are relevant only if there is a preceding name element
  @key nameStart?: number
  @key nameEnd?: number // this is 0 if there is no name
  @key tblStart: number // this is 0 if there is no name
}

const templateNames = new Set([
  "headline",
  "text",
  "source",
  "results",
  "html",
  "drawer",
  "keyword",
  "table",
])

type TemplateFunc = (opts: any) => string

const Handlebars = WINDOW.Handlebars as any
const compile = Handlebars.compile as (template: string) => TemplateFunc

export async function parseOrg(url: string) {
  const text = await (await fetch(url)).text()
  const result = await (
    await fetch(new URL(ORG_PARSE, document.location.href).toString(), {
      method: "POST",
      body: text,
    })
  ).json()
  return result
}

function isHeadline(ch: Chunk): ch is Headline {
  return ch.type === "headline"
}

function isBlock(ch: Chunk): ch is Source {
  return ch.type === "block"
}

function isSource(ch: Chunk): ch is Source {
  return ch.type === "source"
}

function isKeyword(ch: Chunk): ch is Keyword {
  return ch.type === "keyword"
}

function isDrawer(ch: Chunk): ch is Drawer {
  return ch.type === "drawer"
}

export function renderText(text: string) {
  const orig = text
  let pos = 0
  let result = ""

  if (text.startsWith("\n")) {
    result = "<br>\n"
    text = text.slice(1)
  }
  //console.log('render:', text)
  while (text.length) {
    const mark = text.match(MARKUP_RE)
    if (!mark) {
      result += text.slice(pos)
      break
    }
    result += text.slice(pos, mark.index)
    const matched = mark[0]
    switch (matched[0]) {
      case "\n\n":
        result += "<br>\n\n"
      case "*":
        result += `<b>${renderText(matched.slice(1, matched.length - 1))}</b>`
        break
      case "/":
        result += `<i>${renderText(matched.slice(1, matched.length - 1))}</i>`
        break
      case "[":
        const link = mark[1]
        if (link.match(LEISURE_LINK_RE)) {
          let ref = link.match(LEISURE_LINK_RE)[1]
          let namespace = "default"
          let slashInd = ref.indexOf("/")

          if (slashInd != -1) {
            namespace = ref.slice(slashInd + 1)
            ref = ref.slice(0, slashInd)
          }
          result += `<div class='leisure-view' data-view='${ref}' data-namespace='${namespace}'></div>`
        } else if (link.match(HREF_LINK_RE)) {
          result += `<a href='${mark[1]}'>${mark[2] || ""}</span>`
        } else {
          result += `<span data-link-ref='${mark[1]}'>${mark[2] || ""}</span>`
        }
        break
    }
    text = text.slice(mark.index + matched.length)
  }
  if (orig.endsWith("\n\n")) {
    result += "<br>"
  }
  return result
}

function activateAll(el: HTMLElement) {
  if (el.closest("script")) {
    activate(el as HTMLScriptElement)
  }
  for (const scr of el.querySelectorAll(
    "script"
  ) as any as HTMLScriptElement[]) {
    activate(scr as HTMLScriptElement)
  }
}

function activate(script: HTMLScriptElement) {
  const scriptParent = script.parentNode
  const scriptNext = script.nextSibling
  script.remove()
  const newScript = document.createElement("script")
  for (const name of script.getAttributeNames()) {
    newScript.setAttribute(name, script.getAttribute(name))
  }
  newScript.innerHTML = script.innerHTML
  scriptParent.insertBefore(newScript, scriptNext)
}

function optionValue(chunk: Block, op: string) {
  if (chunk.options) {
    const opPos = chunk.options?.indexOf(op)
    if (opPos > -1 && opPos + 1 < chunk.options.length) {
      const last = chunk.options.findIndex((o, i) => i > opPos && o[0] == ":")

      return chunk.options.slice(
        opPos + 1,
        last > -1 ? last : chunk.options.length
      )
    }
  }
  return []
}

function refFor(chunk: Source) {
  const executor = optionValue(chunk, ":executor")[0]

  if (executor) {
    const ref = optionValue(chunk, ":ref")[0]

    return `${executor.trim()}/${(ref || "0").trim()}`
  }
}

function tagsFor(chunk: Source) {
  let start = -1
  let tagsEnd = -1

  if (chunk.options) {
    start = chunk.options.indexOf(":tags")
    if (start > -1) {
      for (
        tagsEnd = start + 1;
        tagsEnd < chunk.options.length &&
        !chunk.options[tagsEnd].startsWith(":");
        tagsEnd++
      ) {
        // count tags
      }
    }
  }
  return start > -1 ? chunk.options.slice(start + 1, tagsEnd) : []
}

function runChunk(
  chunk: Chunk,
  scripts: HTMLScriptElement[],
  dom?: HTMLElement
) {
  if (isSource(chunk)) {
    if (chunk.language === "css") {
      const style = document.createElement("style")
      style.setAttribute("org-id", chunk.id)
      style.textContent = chunk.contentStr
      document.head.append(style)
    } else if (chunk.language === "javascript" || chunk.language === "js") {
      const script = document.createElement("script") as HTMLScriptElement
      script.lang = "javascript"
      script.setAttribute("org-id", chunk.id)
      script.textContent = chunk.contentStr
      script.type = "module"
      scripts.push(script)
    }
  } else if (
    isBlock(chunk) &&
    chunk.blockType == "export" &&
    chunk.options[0] == "html"
  ) {
    const parent =
      chunk.options[1] == ":head" ? document.head : dom || document.body
    const container = document.createElement("div") as HTMLDivElement
    container.innerHTML = chunk.contentStr
    if (dom) {
      (dom as any).classList = optionValue(chunk, ":class").join(" ")
    }
    while (container.firstChild) {
      const child = container.firstChild
      parent.appendChild(child)
      if (child instanceof HTMLElement) {
        activateAll(child)
      }
    }
  }
}

function runScripts(scripts: HTMLScriptElement[]) {
  // run scripts first so they can register helpers, partials, etc.
  for (const script of scripts) {
    document.body.append(script)
  }
}

function findall(el, sel) {
  let result = [...el.querySelectorAll(sel)]

  if (el.matches(sel)) result.unshift(el)
  return result
}

function isInput(node: HTMLElement): node is HTMLInputElement {
  return "value" in node
}

function isTextField(node: HTMLElement) {
  return TEXT_FIELD_MATCHERS.findIndex((m) => m(node)) > -1
}

let currentRenderer: OrgRenderer = null

interface Getter {
  (): any
}

interface Setter {
  (value: any): any
}

interface Binding {
  update(): any
}

const CHUNK_TYPES = {
  headline: Headline,
  text: Chunk,
  source: Source,
  block: Block,
  results: Chunk,
  html: Chunk,
  drawer: Drawer,
  keyword: Keyword,
  table: Table,
}

export class OrgRenderer {
  leisure: Leisure
  dom: any
  globals: { [id: string]: any }
  chunks: { [id: string]: Chunk }
  serial: number
  orphans: HTMLDivElement
  refs: { [id: string]: string }
  views: { [key: string]: (obj: any, options?: any) => string }
  namedChunks: { [id: string]: string }
  taggedChunks: { [id: string]: Set<string> }
  bound: { [name: string]: Binding[] }
  nextId: 1
  handlers: { [id: string]: (ch: Chunk)=> void }

  constructor(leisure: Leisure, dom: HTMLElement, templateChunks: any[]) {
    currentRenderer = this
    this.leisure = leisure
    this.dom = dom
    this.chunks = {}
    this.serial = 0
    this.views = {}
    this.namedChunks = {}
    this.refs = {}
    this.taggedChunks = {}
    this.orphans = document.createElement("div")
    this.orphans.style.display = "none"
    this.handlers = {}
    document.body.append(this.orphans)
    // populate chunks first -- this can load more templates
    this.addTemplates(templateChunks)
    console.log(this)
    WINDOW.Leisure = this
  }

  toggleHidden(event: PointerEvent) {
    if ((event.target as HTMLInputElement).checked) {
      document.body.classList.add("show-hidden")
    } else {
      document.body.classList.remove("show-hidden")
    }
  }

  async increment(pathStr: string, amount: number = 1) {
    const [name, ...p] = pathStr.split(".")
    const block = (this.chunks[this.namedChunks[name]] as Source).value
    const last = p[p.length - 1]

    if (block && last) {
      path(block, p.slice(0, p.length - 1))[last] += amount
      const result = (await this.set(name, block)) as any
      if (result.chunk) {
        this.displayChunk(result.chunk)
        if (result.chunk.name) {
          this.showViewNamed(this.dom, result.chunk.name, result.chunk)
        }
      }
      console.log("increment result", result)
    }
  }

  addTemplates(templateChunks: any[]) {
    const scripts = [] as HTMLScriptElement[]

    console.log("template:", templateChunks)
    const chunks: Chunk[] = templateChunks.map(c=> this.populateChunk(c, false))
    for (const chunk of chunks) {
      runChunk(chunk, scripts)
    }
    runScripts(scripts)
  }

  chunkFor(chunk: any, index = true) {
    return new (CHUNK_TYPES[chunk.type] ?? Chunk)(chunk.type, chunk.id)
  }

  connect(result: any) {
    const chunks = result.chunks.map(c => this.chunkFor(c))
    console.log("connect:", chunks)
    for (const chunk of chunks) {
      this.chunks[chunk.id] = chunk
    }
    for (const chunk of result.chunks) {
      this.populateChunk(chunk, false)
    }
    for (const chunk of chunks) {
      this.displayChunk(chunk)
      this.showViews()
    }
    this.clearOrphans()
  }

  clearOrphans() {
    this.orphans.innerHTML = ""
  }

  removeChunk(removed: Chunk) {
    const dom = this.domFor(removed)

    dom && dom.remove()
    if (isSource(removed)) {
      this.untag(removed)
    }
  }

  update(changes: any) {
    console.log('Updating with changes', changes)
    this.serial++
    const all = [] as Chunk[]
    const changed = new Set() as Set<string>
    for (const removed of changes.removed ?? []) {
      this.removeChunk(removed)
    }
    for (const list of [changes.added ?? [], changes.changed ?? []]) {
      for (const chunk of list) {
        this.chunks[chunk.id] = this.chunkFor(chunk)
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
    if (changes.order) {
      this.orderChunks(changes.order, all)
    }
    for (const chunk of all) {
      this.populateChunk(chunk)
    }
    for (const input_chunk of all) {
      const chunk = this.chunks[input_chunk.id]

      this.displayChunk(chunk)
      if (isSource(chunk) && chunk.name) {
        this.showViewNamed(this.dom, chunk.name, chunk)
      }
    }
    this.clearOrphans()
    console.log("updated", all, this)
  }

  orderChunks(chunkOrder: string[], chunks: Chunk[]) {
    const order = {} as { [id: string]: number }
    let pos = 0
    for (const id of chunkOrder) {
      order[id] = pos++
    }
    chunks.sort((a, b) => order[a.id] - order[b.id])
  }

  domFor(chunk: Chunk | string) {
    if (typeof chunk != "string") {
      chunk = chunk.id
    }
    return this.dom.querySelector(
      `[data-leisure-orgid="${chunk}"]`
    ) as HTMLElement
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
    const currentParentDom =
      chunk.parent && dom.parentElement?.closest("[data-leisure-orgId]")
    if (
      prevDom &&
      (prevDom.nextElementSibling == dom || prevDom == currentParentDom)
    ) {
      return
    }
    if (chunk.parent && chunk.parent == prev) {
      const parentContent =
        parentDom && parentDom.querySelector("[data-leisure-headline-content]")
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
    const dom = document.createElement("span")
    dom.setAttribute("data-leisure-orgid", chunk.id)
    return dom
  }

  displayChunk(chunk: Chunk) {
    const dom = this.domFor(chunk) || this.createDomFor(chunk)
    this.placeDom(dom, chunk)
    if (
      dom &&
      dom.getAttribute("data-leisure-type") === "headline" &&
      chunk.type != "headline"
    ) {
      const oldContents = dom.querySelector("[data-leisure-headline-content]")
      if (oldContents) {
        this.orphans.append(oldContents)
      }
    }
    dom.setAttribute("data-leisure-type", chunk.type)
    this.populateChunk(chunk)
    const scripts: HTMLScriptElement[] = []
    runChunk(chunk, scripts, dom)
    runScripts(scripts)
    if (isKeyword(chunk) && chunk.name.toLowerCase() === "title") {
      document.title = chunk.value.trim()
    } else if (
      !(
        isBlock(chunk) &&
        chunk.blockType == "export" &&
        chunk.options[0] == "html"
      )
    ) {
      const children =
        chunk.type === "headline" &&
        dom.getAttribute("data-leisure-type") === "headline" &&
        dom.querySelector("[data-leisure-headline-content]")
      children && children.remove()
      const ch = this.renderChunk(chunk)
      if (ch !== undefined) {
        dom.innerHTML = this.renderChunk(chunk)
        this.bind(dom)
        if (children) {
          while (children.firstChild) {
            dom
              .querySelector("[data-leisure-headline-content]")
              .append(children.firstChild)
          }
        }
      }
    }
    activateAll(dom)
  }

  bind(el: HTMLElement) {
    this.scanAttr(el, "data-text", (_: string, getter: Getter) => ({
      update() {
        el.textContent = String(getter())
      },
    }))
    this.scanAttr(
      el,
      "data-value",
      (base: string, getter: Getter, setter: Setter) => {
        const result = {} as any

        if (isTextField(el)) {
          if (isInput(el)) {
            el.onblur = () => this.set(base, setter(el.value))
            result.update = () => {
              el.value = getter()
              this.validate(el)
            }
          } else {
            el.onblur = () => this.set(base, setter(el.textContent))
            result.update = () => {
              el.textContent = getter()
            }
          }
        }
        return result
      }
    )
    for (const child of el.children as any as HTMLElement[]) {
      this.bind(child)
    }
  }

  chunk(id: string) {
    return this.chunks[id]
  }

  get(name: string) {
    return structuredClone(
      (this.chunks[this.namedChunks[name]] as Source).value
    )
  }

  async set(name: string, value: any) {
    const chunk = this.chunks[this.namedChunks[name]] as Source

    if (chunk) {
      const result = (await this.leisure.set(name, value)) as any
      if (result.chunk) {
        this.displayChunk(result.chunk)
        if (result.chunk.name) {
          this.showViewNamed(this.dom, result.chunk.name, result.chunk)
        }
      }
      console.log("set result", result)
    }
  }

  validate(el: any) {
    if (el.validity) {
      if (!el.validity.valid) {
        el.style.background = "lightpink"
        return false
      } else {
        el.style.removeProperty("background")
      }
    }
    return true
  }

  scanAttr(
    el: HTMLElement,
    attr: string,
    binding:
      | ((base: string, getter: Getter) => Binding)
      | ((base: string, getter: Getter, setter: Setter) => Binding)
  ) {
    for (const node of findall(el, `[${attr}]`)) {
      if (!node.isConnected) continue
      const [base, getter, setter] = this.parseBinding(el.getAttribute(attr))
      if (!base) continue
      binding(base, getter, setter)
    }
  }

  // simple paths -- a src block name with an optional path which can contain indexing
  // returns [ base-src-name, getter-function, setter-function ]
  parseBinding(binding: string) {
    try {
      const [, b, rest] = binding.match(LEISURE_PATH)
      const base = b[0] === "@" ? b.slice(1) : b
      const baseGet =
        b[0] === "@"
          ? () => this.globals[base]
          : () =>
            structuredClone(
              (this.chunks[this.namedChunks[base]] as Source).value
            )
      const baseSet =
        b[0] === "@"
          ? (v) => (this.globals[base] = v)
          : (v) => this.set(base, v)
      const parts = rest.split(".")
      let partInd = 0
      let trunk = null
      let getter = null
      let setter = null

      for (const part in parts) {
        const [, prop, index] = part.match(/^([^\[\]]+)(?:\[([^\[\]]+)\])?$/)

        if (partInd === parts.length - 1) {
          if (!trunk) {
            setter = !index
              ? baseSet
              : b[0] === "@"
                ? (value: any) => {
                  this.globals[base] = value
                }
                : (value: any) => {
                  const obj = structuredClone(
                    (this.chunks[this.namedChunks[base]] as Source).value
                  )

                  obj[part][index] = value
                  this.set(base, obj)
                }
          } else {
            ///////////////////////
            //// FIX
            const prev = trunk
            trunk = !index
              ? (value: any) => (prev(this.leisure.get(base))[part] = value)
              : (value: any) =>
                (prev(this.leisure.get(base))[part][index] = value)
          }
        }
        if (!trunk) {
          trunk = !index
            ? (obj: any) => obj[part]
            : (obj: any) => obj[part][index]
        } else {
          const prev = trunk
          trunk = !index
            ? (obj: any) => prev(obj)[part]
            : (obj: any) => prev(obj)[part][index]
        }
        if (partInd === parts.length - 1) {
          getter = () => trunk(this.leisure.get(base))
        }
        partInd++
      }
      //// FIX
      ///////////////////////
      return [base, getter, setter]
    } catch (err) {
      console.log(`Error parsing Leisure binding '${binding}':`, err)
      return null
    }
  }

  elId(el: HTMLElement) {
    if (!el.hasAttribute("data-leisure-id")) {
      el.setAttribute("data-leisure-id", String(this.nextId++))
    }
    return el.getAttribute("data-leisure-id")
  }

  showViews() {
    const names = new Set<string>()

    for (const div of this.dom.querySelectorAll("[data-view]")) {
      names.add(div.getAttribute("data-view"))
    }
    for (const name of names) {
      this.showViewNamed(this.dom, name)
    }
  }

  showViewNamed(dom, name: string, chunk: Source = null) {
    if (!chunk) {
      chunk = this.chunks[this.namedChunks[name]] as Source
    }
    if (chunk.valueType) {
      for (const div of dom.querySelectorAll(`[data-view=${name}]`)) {
        const view =
          this.renderView(
            chunk.value,
            chunk.valueType,
            div.getAttribute("data-namespace"),
            chunk
          ) || ""

        div.innerHTML = view
      }
    }
  }

  renderView(
    value: any,
    type: string,
    namespace: string = "default",
    chunk?: Source
  ) {
    const view = this.views[`${type}/${namespace}`]

    if (chunk) {
      console.log('RENDER CHUNK:', chunk)
      return view && view(value, { data: { chunk } })
    }
    console.log('RENDER CHUNK:', value)
    return view && view(value)
  }

  // add properties to chunk to support templates
  populateChunk(raw: any, index = true) {
    let chunk = this.chunks[raw.id] || this.chunkFor(raw)
    chunk.mainPopulate(this, raw, index)
    chunk.serial = this.serial
    return chunk
  }

  tag(chunk: Source) {
    const tags = tagsFor(chunk)

    if (tags?.length) {
      chunk.tags = tags
      for (const tag of tags) {
        const tags = this.taggedChunks[tag]

        if (!tags) {
          this.taggedChunks[tag] = new Set([chunk.id])
        } else {
          tags.add(chunk.id)
        }
      }
    }
  }

  untag(chunk: Source) {
    for (const tag of tagsFor(chunk)) {
      const tags = this.taggedChunks[tag]

      if (!tags || !tags.has(chunk.id)) {
        continue
      } else if (tags.size == 1) {
        delete this.taggedChunks[tag]
      } else {
        tags.delete(chunk.id)
      }
    }
  }

  scanView(src: Source) {
    const match = src.raw.begin.match(VIEW_RE)
    if (!match) {
      return
    }
    let [, viewType, viewNamespace] = match
    viewNamespace = viewNamespace ? viewNamespace : "default"
    this.views[`${viewType}/${viewNamespace}`] = compile(src.raw.content)
  }

  renderChunk(chunk: Chunk) {
    if (this.handlers[chunk?.id]) {
      this.handlers[chunk.id](chunk)
      return
    }
    const template = this.views[`Leisure.${chunk.type}/default`]
    if (template) {
      console.log('RENDER CHUNK', chunk)
      return template(chunk)
    }
    return renderText(chunk.text)
  }
}

///
/// Handlebars handlers
///

console.log("adding renderText handler")
// render org text
Handlebars.registerHelper("renderText", renderText)

Handlebars.registerHelper("options", (chunk, opts)=> {
  console.log('OPTIONS HELPER, CHUNK:', chunk, 'OPTS: ', opts)
  return chunk.text.slice(chunk.label, chunk.labelEnd)
})

// view, render a view or an object
Handlebars.registerHelper("view", (item: any, namespace: string) => {
  if (!currentRenderer || !item.LEISURE_TYPE) return
  if (typeof namespace != "string") {
    namespace = "default"
  }
  return currentRenderer.renderView(item, item.LEISURE_TYPE, namespace)
})

// get blocks with all of the given tags
Handlebars.registerHelper("withTags", (...tags: any[]) =>
  withAllTags(...(tags.slice(0, tags.length - 1) as string[])).map(
    (obj: any) => obj.value
  )
)

// get blocks with any of the given tags
Handlebars.registerHelper("withAnyTags", (...tags: any[]) =>
  withAnyTags(...(tags.slice(0, tags.length - 1) as string[])).map(
    (obj: any) => obj.value
  )
)

//less-than comparison
Handlebars.registerHelper("lt", (a: any, b?: any) => {
  return emfunction(a, b, (a) => a < b)
})

//not
Handlebars.registerHelper("not", (a: any) => {
  if (typeof a === "function") {
    return (obj: any) => !a(obj)
  }
  return !a
})

//greater-than comparison
Handlebars.registerHelper("gt", (a: any, b?: any) => {
  return emfunction(a, b, (a) => a > b)
})

//filter a collection
Handlebars.registerHelper("filter", (items: any, func: (obj: any) => any) => {
  if (!Array.isArray(items)) {
    const newArray = []
    for (const item of items) {
      newArray.push(item)
    }
    items = newArray
  }
  return items.filter(func)
})

//map a collection
Handlebars.registerHelper("map", (items: any, func: (obj: any) => any) => {
  if (!Array.isArray(items)) {
    const newArray = []
    for (const item of items) {
      newArray.push(item)
    }
    items = newArray
  }
  return items.map(func)
})

function emfunction(a: string, b: any, func: (a: any) => any) {
  if (typeof a === "string" && typeof b !== "string") {
    return (obj: any) => func(path(obj, a))
  }
  return func(a)
}

function path(root: any, pathStr: string | string[]) {
  const path =
    typeof pathStr === "string" ? pathStr.split(".") : (pathStr as string[])
  let pos = 0

  for (; root && pos < path.length; pos++) {
    root = root[path[pos]]
  }
  return root
}

function withAllTags(...tags: string[]) {
  if (!currentRenderer) return []
  const first = tags[0]
  tags = tags.slice(1)
  const result: Source[] = []
  chunk: for (const id of currentRenderer.taggedChunks[first]) {
    const blk = currentRenderer.chunks[id] as Source
    for (const tag of blk.tags || []) {
      if (!blk.tags?.includes(tag)) continue chunk
    }
    result.push(blk)
  }
  return result
}

function withAnyTags(...tags: string[]) {
  if (!currentRenderer) return []
  const blocks = new Set<Source>()
  for (const tag of tags) {
    for (const id of currentRenderer.taggedChunks[tag]) {
      blocks.add(currentRenderer.chunks[id] as Source)
    }
  }
  const result: Source[] = []
  for (const blk of blocks) {
    result.push(blk)
  }
  return result
}
