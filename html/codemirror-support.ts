import { EditorView, basicSetup } from 'codemirror'
import { EditorState } from '@codemirror/state'
import { javascript } from '@codemirror/lang-javascript'
import { yaml } from '@codemirror/lang-yaml'
import { json } from '@codemirror/lang-json'
import { julia } from '@plutojl/lang-julia'
import interact from '@replit/codemirror-interact'
import { OrgRenderer, Chunk, Source } from './orgRenderer'

//export const CodeMirrorInteract = interact.default;

console.log('INTERACT', interact)

//console.log('CodeMirrorInteract', CodeMirrorInteract)

//const {EditorView, basicSetup, javascript, yaml, json} = CodeMirror

let cmCount = 0

const LANGS = {
  js: javascript,
  javascript,
  yaml,
  json,
  julia,
}

function chomp(str) {
  return str.length > 0 && str[str.length - 1] == '\n' ? str.slice(0, str.length - 1) : str
}

function ensureNL(str) {
  return str.length > 0 && str[str.length - 1] == '\n' ? str : str + '\n'
}

function Leisure(): OrgRenderer {
  return (window as any).Leisure
}

function makePlainEditor(parent: HTMLElement, doc: string, input: () => void) {
  return new EditorView({
    extensions: [
      EditorView.updateListener.of(function (e) {
        if (e.docChanged) {
          input()
        }
      }),
    ],
    doc,
    parent,
  })
}

// from: https://github.com/replit/codemirror-interact/blob/master/dev/index.ts
const hex2rgb = (hex: string): [number, number, number] => {
  const v = parseInt(hex.substring(1), 16)
  return [(v >> 16) & 255, (v >> 8) & 255, v & 255]
}

// from: https://github.com/replit/codemirror-interact/blob/master/dev/index.ts
const rgb2hex = (r: number, g: number, b: number): string =>
  '#' + r.toString(16) + g.toString(16) + b.toString(16)

function makeCodeEditor(
  lang: string,
  parent: HTMLElement,
  doc: string,
  input: () => void,
) {
  const extensions = [
    EditorView.updateListener.of(function (e) {
      if (e.docChanged) {
        input()
      }
    }),
    //CodeMirrorInteract({
    interact({
      rules: [
        // NOTE: number dragger, bool toggler, vec2 slider, and color picker copied from
        //       example code in the codemirror-interact Git repo:
        //      https://github.com/replit/codemirror-interact/blob/master/dev/index.ts
        //
        // a rule for a number dragger
        {
          // the regexp matching the value
          regexp: /-?\b\d+\.?\d*\b/g,
          // set cursor to "ew-resize" on hover
          cursor: 'ew-resize',
          // change number value based on mouse X movement on drag
          onDrag: (text, setText, e) => {
            const newVal = Number(text) + e.movementX
            if (isNaN(newVal)) return
            setText(newVal.toString())
          },
        },
        // bool toggler
        {
          regexp: /true|false/g,
          cursor: 'pointer',
          onClick: (text, setText) => {
            switch (text) {
              case 'true':
                return setText('false')
              case 'false':
                return setText('true')
            }
          },
        },
        // kaboom vec2 slider
        {
          regexp: /vec2\(-?\b\d+\.?\d*\b\s*(,\s*-?\b\d+\.?\d*\b)?\)/g,
          cursor: 'move',
          onDrag: (text, setText, e) => {
            const res =
              /vec2\((?<x>-?\b\d+\.?\d*\b)\s*(,\s*(?<y>-?\b\d+\.?\d*\b))?\)/.exec(
                text,
              )
            const x = Number(res?.groups?.x)
            let y = Number(res?.groups?.y)
            if (isNaN(x)) return
            if (isNaN(y)) y = x
            setText(`vec2(${x + e.movementX}, ${y + e.movementY})`)
          },
        },
        // kaboom color picker
        {
          regexp: /rgb\(.*\)/g,
          cursor: 'pointer',
          onClick: (text, setText, e) => {
            const res = /rgb\((?<r>\d+)\s*,\s*(?<g>\d+)\s*,\s*(?<b>\d+)\)/.exec(
              text,
            )
            const r = Number(res?.groups?.r)
            const g = Number(res?.groups?.g)
            const b = Number(res?.groups?.b)
            const sel = document.createElement('input')
            sel.type = 'color'
            if (!isNaN(r + g + b)) sel.value = rgb2hex(r, g, b)
            sel.addEventListener('change', (e) => {
              const el = e.target as HTMLInputElement
              if (el.value) {
                const [r, g, b] = hex2rgb(el.value)
                setText(`rgb(${r}, ${g}, ${b})`)
              }
            })
            sel.click()
          },
        },
      ],
    }),
    basicSetup,
  ]

  if (LANGS[lang]) {
    extensions.push(LANGS[lang]())
  }
  return new EditorView({
    state: EditorState.create({
      extensions,
      doc,
    }),
    parent,
  })
}

function updateEditors(ch: Chunk) {
  const els = document.querySelectorAll(
    `[data-leisure-orgid=${ch.id}] code-mirror`,
  )

  console.log('INCOMING UPDATE', ch)
  for (const el of els as unknown as CodeMirrorElement[]) {
    el.update()
  }
}

class CodeMirrorElement extends HTMLElement {
  view: EditorView
  getContents: (ch: Source) => string
  setContents: (ch: Source) => void
  updating: boolean

  constructor() {
    super()
    this.getContents = () => ''
    this.setContents = () => {}
  }

  connectedCallback() {
    if (!this.view) {
      this.createEditor()
    }
  }

  leisureId(): string {
    return this.closest('[data-leisure-orgid]')?.getAttribute(
      'data-leisure-orgid',
    )
  }

  chunk(): Source {
    return Leisure().chunk(this.leisureId()) as Source
  }

  language() {
    return this.chunk().language
  }

  plain() {
    return 'plain' in (this.attributes as any)
  }

  createEditor() {
    console.log(
      `Creating editor language ${this.language()} document: ${this.getContents(this.chunk())} plain: ${this.plain()}`,
    )
    this.handleBind()
    const input = () => {
      if (this.setContents) {
        this.setContents(this.chunk())
      }
    }
    if (this.plain()) {
      this.view = makePlainEditor(this, this.getContents(this.chunk()), input)
    } else {
      this.view = makeCodeEditor(
        this.language(),
        this,
        this.getContents(this.chunk()),
        input,
      )
    }
    Leisure().handlers[this.leisureId()] = updateEditors
  }

  disconnectedCallback() {}

  adoptedCallback() {
    if (!this.view) {
      this.createEditor()
    }
  }

  async whileUpdating(func: ()=> Promise<any>) {
    if (this.updating) {
      return
    }
    try {
      this.updating = true
      await func()
    } finally {
      this.updating = false
    }
  }

  update() {
    if (this.view) {
      this.whileUpdating(async ()=> {
        console.log('UPDATE', this)
        this.view.dispatch({
          changes: {
            from: 0,
            to: this.view.state.doc.length,
            insert: this.getContents(this.chunk()),
          },
        })
      })
    }
  }

  async chunkReplace(start: number, end: number, str?: string) {
    this.whileUpdating(async ()=> {
      const ch = this.chunk()
      const txt = ch.text
      if (str === undefined) {
        str = this.view.state.doc.toString()
      }
      await Leisure().leisure.doEdits({
        selectionOffset: -1,
        selectionLength: -1,
        replacements: [
          {
            block: this.leisureId(),
            text: txt.slice(0, start) + str + txt.slice(end),
          },
        ],
      })
    })
  }

  setName(n: string) {
    this.chunkReplace
  }

  attributeChangedCallback(name: string, _oldValue: string, newValue: string) {
    console.log(`attribute ${name} changed from ${_oldValue} to ${newValue}`)
    switch (name) {
      case 'plain':
        this.createEditor()
        break
      case 'bind':
        this.handleBind()
        break
    }
  }

  handleBind() {
    console.log('BIND: ', this.getAttribute('bind'), this)
    switch (this.getAttribute('bind')) {
      case 'name':
        this.getContents = (ch) => ch.name
        this.setContents = (ch) =>
          this.chunkReplace(ch.nameStart || 0, ch.nameEnd || 0)
        break
      case 'label':
        //this.getContents = (ch) => ch.text.slice(ch.label, ch.labelEnd)
        //this.setContents = (ch) => this.chunkReplace(ch.label, ch.labelEnd)
        this.getContents = (ch) => ch.text.slice(ch.label, ch.content - 1)
        this.setContents = (ch) => this.chunkReplace(ch.label, ch.content - 1)
        break
      case 'content':
        this.getContents = (ch) => chomp(ch.contentStr || '')
        this.setContents = (ch) => this.chunkReplace(ch.content, ch.end, ensureNL(this.view.state.doc))
        break
      default:
        throw new Error(`BAD CODE-MIRROR BIND VALUE: ${this.getAttribute('bind')}`)
        break
    }
    this.update()
  }
}

export function initCodemirror() {
  customElements.define('code-mirror', CodeMirrorElement)
}
