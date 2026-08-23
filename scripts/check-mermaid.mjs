// 校验 docs/ 下所有 ```mermaid 代码块的语法（mermaid.parse + jsdom 环境）。
// VitePress 构建不校验 mermaid 语法，错误只会在浏览器渲染时暴露，故独立把关。
// 用法：node scripts/check-mermaid.mjs
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { JSDOM } from 'jsdom'

const dom = new JSDOM('<!DOCTYPE html><body></body>', { pretendToBeVisual: true })
globalThis.window = dom.window
globalThis.document = dom.window.document
// Node 21+ 自带只读 navigator，无需注入
if (!globalThis.navigator) globalThis.navigator = dom.window.navigator

const { default: mermaid } = await import('mermaid')
mermaid.initialize({ startOnLoad: false, suppressErrorRendering: true })

const docsDir = join(process.cwd(), 'docs')
const blocks = []

function walk(dir) {
  for (const name of readdirSync(dir)) {
    if (name.startsWith('.') || name === 'issues' || name.startsWith('_')) continue
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p)
    else if (name.endsWith('.md')) collect(p)
  }
}

function collect(file) {
  const src = readFileSync(file, 'utf8')
  const re = /```mermaid\n(.*?)```/gs
  let m
  while ((m = re.exec(src))) {
    blocks.push({ file: relative(process.cwd(), file), code: m[1] })
  }
}

walk(docsDir)

let failed = 0
for (const b of blocks) {
  try {
    await mermaid.parse(b.code)
  } catch (e) {
    failed++
    const firstLine = b.code.split('\n')[0]
    console.error(`✗ ${b.file}\n    ${firstLine}\n    ${String(e.message ?? e).split('\n')[0]}`)
  }
}
console.log(`\nmermaid 块共 ${blocks.length} 个，语法错误 ${failed} 个`)
process.exit(failed ? 1 : 0)
