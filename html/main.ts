import { Leisure, Edit, Replacement } from './leisure.js'
import { OrgRenderer, parseOrg } from './orgRenderer.js'

const DEFAULT_TEMPLATES = "/default.org"

async function replace(json: Edit) {
    return replacements(json.replacements)
}

async function connect(result: any) {
  if (Array.isArray(result)) {
    return replacements(result as Replacement[])
  } else if (typeof result === 'string') {
    return replacements([{offset: -1, length: -1, text: result}])
  } else {
    document.body.innerHTML = `Unknown result type: <pre>${result}</pre>`
  }
}

async function replacements(repls: Replacement[]) {
  const slices = []
  let prev = document.body.textContent
  
  for (let {offset, length, text} of repls) {
    if (offset == -1) {
      offset = 0
      length = document.body.textContent.length
    }
    slices.push(prev.slice(0, offset))
    if (text) {
      slices.push(text)
    }
    prev = prev.slice(offset + length)
  }
  if (prev) {
    slices.push(prev)
  }
  document.body.textContent = slices.join('')
}

function updates(): Edit {
    return {selectionOffset: 0, selectionLength: 0, replacements: []}
}

function displayError(err: any) {
  document.body.textContent = err.toString()
}

async function ready() {
  if (document.readyState !== "complete") {
    return
  }
  const docUrl = new URL(document.location.href)
  if (!docUrl.searchParams.has("doc")) {
    document.body.textContent = `No "doc" parameter`
    return
  }
  const leisure = new Leisure(docUrl.href, "browser", docUrl.searchParams.get("doc"))
  //await leisure.connect(replacements)
  //leisure.updateLoop(updates, replace, displayError)
  const renderer = new OrgRenderer(await parseOrg(docUrl.searchParams.get("templates") || DEFAULT_TEMPLATES))
  await leisure.connect((repl)=> renderer.connect(repl))
  leisure.updateLoop(updates, (repl)=>renderer.update(repl), displayError)
}

if (document.readyState === "complete") {
  ready()
} else {
  document.onreadystatechange = ready
}
