//import {EditorJS} from './support.js'
import * as ejs from '@editorjs/editorjs'
export const EditorJS = ejs.default;

import {renderText} from './orgRenderer.js'

console.log('EditorJS', EditorJS)

class TextEditorElement extends HTMLElement {
  view

  constructor() {
    super()
  }
  
  connectedCallback() {
    this.createEditor()
  }

  bind() {
    return this.attributes['bind']?.value || ''
  }

  doc() {
    return this.attributes['document']?.value || ''
  }

  createEditor() {
    if (!this.view) {
      let chunk = this.closest(`[data-leisure-orgid]`)
      if (chunk) {
        this.id = chunk.attributes['data-leisure-orgid'].value + '-editor'
        console.log(`MAKE EDITOR WITH DOC ${this.doc()}`)
        this.view = new EditorJS({
          holderId: this.id,
          minHeight: 16,
          data: {
            time: Date.now(),
            blocks: [
              {
                type: 'paragraph',
                data: {
                  text: this.doc(),
                },
              },
            ],
          },
        })
      }
    }
  }

  disconnectedCallback() {}

  adoptedCallback() {}

  attributeChangedCallback(name:String, _oldValue:String, newValue:String) {
    console.log(`attribute ${name} changed from ${_oldValue} to ${newValue}`)
    switch (name) {
      case 'document':
        if (this.view) {
          //this.view.dispatch({changes: {from: 0, to: this.doc().length, insert: newValue}})
        }
    }
  }
}

export function initTextEditor() {
  console.log('ADDING TEXT EDITOR')
  customElements.define('text-editor', TextEditorElement, )
}
