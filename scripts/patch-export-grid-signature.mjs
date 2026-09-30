// 一次性重构脚本：把 ExportInvoiceGrid 拆成 Detailed 版本 + 薄包装，
// 并加 GridExport / ErrNoUsableInvoiceFile。用完即可删。
import { readFileSync, writeFileSync } from 'node:fs'

const p = 'C:/workspace/openpocket/backend/internal/email/export_pdf.go'
let src = readFileSync(p, 'utf8')

const tailOld = [
  '\t_ = merged',
  '\texportedCount, exportedSkipped = countNormalized(normalized, skipped)',
  '\t_ = merged',
  '\t_ = exportedCount',
  '\t_ = exportedSkipped',
  '\treturn outFile, nil',
].join('\r\n')
const tailNew = '\treturn &GridExport{Path: outFile, Count: len(normalized), Skipped: skipped}, nil'
if (!src.includes(tailOld)) throw new Error('tail block not found')
src = src.replace(tailOld, tailNew)

const sigOld = 'func ExportInvoiceGrid(outDir string, invoiceFiles []string, grid int) (out string, err error) {'
const sigNew = [
  '// ExportInvoiceGrid 把 invoiceFiles 合并为 A4 网格 PDF，返回输出文件绝对路径。',
  'func ExportInvoiceGrid(outDir string, invoiceFiles []string, grid int) (string, error) {',
  '\tres, err := ExportInvoiceGridDetailed(outDir, invoiceFiles, grid)',
  '\tif err != nil {',
  '\t\treturn "", err',
  '\t}',
  '\treturn res.Path, nil',
  '}',
  '',
  '// ExportInvoiceGridDetailed 同 ExportInvoiceGrid，但额外返回实际入网格的张数与被跳过的文件名。',
  'func ExportInvoiceGridDetailed(outDir string, invoiceFiles []string, grid int) (res *GridExport, err error) {',
].join('\r\n')
if (!src.includes(sigOld)) throw new Error('signature not found')
src = src.replace(sigOld, sigNew)

// Detailed 内部的返回值要从 "" 改成 nil
const bodyStart = src.indexOf('func ExportInvoiceGridDetailed')
const bodyEnd = src.indexOf('\n}\r\n', bodyStart)
let body = src.slice(bodyStart, bodyEnd)
body = body.replace(/return "", /g, 'return nil, ')
body = body.replace('out, err = nil, fmt.Errorf("export aborted', 'res, err = nil, fmt.Errorf("export aborted')
src = src.slice(0, bodyStart) + body + src.slice(bodyEnd)

// 类型 + 哨兵
const anchor = '// ExportInvoiceGrid 把 invoiceFiles 合并为 A4 网格 PDF，返回输出文件绝对路径。'
const decl = [
  '// GridExport 是一次 A4 网格导出的结果。Count 是**真正进入网格**的张数，',
  '// 不是请求里勾选的张数——畸形/不可解析的附件会被跳过，报请求数会骗人。',
  'type GridExport struct {',
  '\tPath    string   `json:"-"`',
  '\tCount   int      `json:"count"`',
  '\tSkipped []string `json:"skipped,omitempty"`',
  '}',
  '',
  '// ErrNoUsableInvoiceFile 表示选中的发票文件一个都用不了（全是畸形 PDF/图片）。',
  '// 调用方应回 400 而不是 500：这是用户选择/上游附件的问题，不是服务故障。',
  'var ErrNoUsableInvoiceFile = errors.New("email: no usable invoice file to export")',
  '',
].join('\r\n')
src = src.replace(anchor, decl + anchor)

if (!src.includes('"errors"')) {
  src = src.replace('\t"fmt"\r\n', '\t"errors"\r\n\t"fmt"\r\n')
}
writeFileSync(p, src, 'utf8')
console.log('rewritten ok')
