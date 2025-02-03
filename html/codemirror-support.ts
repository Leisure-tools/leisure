import {EditorView, basicSetup} from 'codemirror'
import { javascript } from "@codemirror/lang-javascript";
import { yaml } from "@codemirror/lang-yaml";
import { json } from "@codemirror/lang-json";
import { julia } from "@plutojl/lang-julia";
import * as interact from '@replit/codemirror-interact'

export const CodeMirrorInteract = interact.default;

console.log('CodeMirrorInteract', CodeMirrorInteract)

//const {EditorView, basicSetup, javascript, yaml, json} = CodeMirror

let cmCount = 0

const LANGS = {
  js: javascript,
  javascript,
  yaml,
  json,
  julia,
};

function makePlainEditor(parent, doc) {
  return new EditorView({
    extensions: [],
    doc,
    parent,
  });
}

// from: https://github.com/replit/codemirror-interact/blob/master/dev/index.ts
const hex2rgb = (hex: string): [number, number, number] => {
  const v = parseInt(hex.substring(1), 16);
  return [
    (v >> 16) & 255,
    (v >> 8) & 255,
    v & 255,
  ];
}

// from: https://github.com/replit/codemirror-interact/blob/master/dev/index.ts
const rgb2hex = (r: number, g: number, b: number): string =>
  '#' + r.toString(16) + g.toString(16) + b.toString(16);

function makeCodeEditor(lang, parent, doc) {
  let extensions = [
    basicSetup,
    CodeMirrorInteract({
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
          cursor: "ew-resize",
          // change number value based on mouse X movement on drag
          onDrag: (text, setText, e) => {
            const newVal = Number(text) + e.movementX;
            if (isNaN(newVal)) return;
            setText(newVal.toString());
          },
        },
        // bool toggler
        {
          regexp: /true|false/g,
          cursor: 'pointer',
          onClick: (text, setText) => {
            switch (text) {
              case 'true': return setText('false');
              case 'false': return setText('true');
            }
          },
        },
        // kaboom vec2 slider
        {
          regexp: /vec2\(-?\b\d+\.?\d*\b\s*(,\s*-?\b\d+\.?\d*\b)?\)/g,
          cursor: "move",
          onDrag: (text, setText, e) => {
            const res = /vec2\((?<x>-?\b\d+\.?\d*\b)\s*(,\s*(?<y>-?\b\d+\.?\d*\b))?\)/.exec(text);
            let x = Number(res?.groups?.x);
            let y = Number(res?.groups?.y);
            if (isNaN(x)) return;
            if (isNaN(y)) y = x;
            setText(`vec2(${x + e.movementX}, ${y + e.movementY})`);
          },
        },
        // kaboom color picker
        {
          regexp: /rgb\(.*\)/g,
          cursor: "pointer",
          onClick: (text, setText, e) => {
            const res = /rgb\((?<r>\d+)\s*,\s*(?<g>\d+)\s*,\s*(?<b>\d+)\)/.exec(text);
            const r = Number(res?.groups?.r);
            const g = Number(res?.groups?.g);
            const b = Number(res?.groups?.b);
            const sel = document.createElement("input");
            sel.type = "color";
            if (!isNaN(r + g + b)) sel.value = rgb2hex(r, g, b);
            sel.addEventListener("change", (e) => {
              const el = e.target as HTMLInputElement;
              if (el.value) {
                const [r, g, b] = hex2rgb(el.value);
                setText(`rgb(${r}, ${g}, ${b})`)
              }
            });
            sel.click();
          },
        },
      ],
    }),
  ]

  if (LANGS[lang]) {
    extensions.push(LANGS[lang]());
  }
  return new EditorView({
    extensions,
    doc,
    parent,
  });
}

class CodeMirrorElement extends HTMLElement {
  view

  constructor() {
    super()
  }
  
  connectedCallback() {
    if (!this.view) {
      this.createEditor()
    }
  }

  leisure_id() {
    return this.attributes['leisure-id']?.value || ''
  }

  bind() {
    return this.attributes['bind']?.value || ''
  }

  language() {
    return this.attributes['language']?.value || ''
  }

  plain() {
    return 'plain' in (this.attributes as any)
  }

  doc() {
    let doc = this.attributes['document']?.value || ''
    if (doc.length > 0 && doc[doc.length - 1] == '\n') {
      return doc.slice(0, doc.length - 1)
    }
    return doc
  }

  createEditor() {
    console.log(`Creating editor language ${this.language()} document: ${this.doc()} plain: ${this.plain()}`)
    if (this.plain()) {
      this.view = makePlainEditor(this, this.doc())
    } else {
      this.view = makeCodeEditor(this.language(), this, this.doc())
    }
  }

  disconnectedCallback() {}

  adoptedCallback() {}

  attributeChangedCallback(name:String, _oldValue:String, newValue:String) {
    console.log(`attribute ${name} changed from ${_oldValue} to ${newValue}`)
    switch (name) {
      case 'language':
        this.createEditor()
        break
      case 'plain':
        this.createEditor()
        break
      case 'document':
        if (this.view) {
          this.view.dispatch({changes: {from: 0, to: this.doc().length, insert: newValue}})
        }
    }
  }
}

export function initCodemirror() {
  customElements.define('code-mirror', CodeMirrorElement, )
}
