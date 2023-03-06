const HEADLINE_RE = /^(\*+)( +)(.*)$/;
const HTML_RE = /^(#+name:[^\n]*\n)?(.*)(#+begin_html[^\n]*\n)(.*\n)(#+end_html[^\n]*\n)/is;
const SOURCE_RE = /^(#+name:[^\n]*\n)?(.*)(#+begin_src *([^ \n]*)[^\n]*\n)(.*\n)(#+end_src[^\n]*\n)(.*)(\n#+result:.*)?$/is;
const MARKUP_RE = /\*[^*]+\*|\/[^/]+\//

interface Chunk {
  type: string
  id: string
  text: string
  next?: string
  prev?: string
  parent?: string
  children?: string[]
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

class OrgRenderer {
  templates: {[t: string]: TemplateFunc}

  constructor(templateChunks: Chunk[]) {
    const scripts = [] as HTMLScriptElement[]
    const templates = [] as Source[]
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
      this.templates[template.name] = compile(template.contentStr)
    }
  }

  domFor(chunk: Chunk | string) {
    if (typeof chunk != 'string') {
      chunk = chunk.id
    }
    return document.querySelector(`[x-orgid="${chunk}"]`) as HTMLElement
  }

  createDomFor(chunk: Chunk) {
    const dom = document.createElement('div')
    if (chunk.parent && chunk.parent == chunk.prev) {
      const parent = this.domFor(chunk.parent)
      if (parent && parent.firstChild) {
        // insert dom after the parent's inner node
        parent.insertBefore(dom, parent.firstChild.nextSibling)
        return dom
      }
    } else if (chunk.prev) {
      const prev = this.domFor(chunk.prev)
      prev.after(dom)
    } else {
      document.body.insertBefore(dom, document.body.firstChild)
    }
    return dom
  }

  displayChunk(chunk: Chunk) {
    const dom = this.domFor(chunk) || this.createDomFor(chunk)
    this.populateChunk(chunk)
    dom.innerHTML = this.renderChunk(chunk)
  }

  // add properties to chunk to support templates
  populateChunk(chunk: Chunk) {
    switch (chunk.type) {
	  case 'headline': {
        const hl = chunk as Headline
        const [, lvl, inter, content] = chunk.text.match(HEADLINE_RE)
        hl.levelStr = lvl
        hl.interStr = inter
        hl.contentStr = content
        hl.hlClass = hl.level < 5 ? `leisure-hl-${hl.level}` : 'leisure-hl-deep'
        break
      }
	  case 'source': {
        const src = chunk as Source
        const [, name, inter1, begin, language, content, end, inter2, result] = chunk.text.match(SOURCE_RE)
        src.name = name
        src.language = language.toLowerCase()
        src.beginStr = begin
        src.inter1Str = inter1
        src.contentStr = content
        src.endStr = end
        src.inter2Str = inter2
        src.resultStr = result
      }
      case 'block': {
        const bl = chunk as Block
        bl.contentStr = bl.text.slice(bl.content, bl.end - 1)
        break
      }
	  case 'drawer':
        break
	  default:
        break
    }
  }

  renderChunk(chunk: Chunk) {
    //switch (chunk.type) {
	//  case 'headline':
    //    let [, lvl, hdl] = chunk.text.match(HEADLINE_RE)
    //    const levelClass = lvl.length > 5 ? 'hl-low' : `hl-${lvl.length}`
    //    return `<span class='${levelClass}'>${}</span>`
	//  case 'text':
	//  case 'source':
	//  case 'results':
	//  case 'html':
	//  case 'drawer':
	//  case 'keyword':
	//  case 'table':
    //}
    return ''
  }
}
