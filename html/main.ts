import { Leisure, Edit, Replacement } from './leisure.js'

async function replace(json: Edit) {
    return replacements(json.replacements)
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
  if (!new URL(document.location.href).searchParams.has("doc")) {
    document.body.textContent = `No "doc" parameter`
    return
  }
  const l = new Leisure(document.location.href, "browser",
                        new URL(document.location.href).searchParams.get("doc"))
  await l.connect(replacements)
  l.updateLoop(updates, replace, displayError)
}

if (document.readyState === "complete") {
  ready()
} else {
  document.onreadystatechange = ready
}
