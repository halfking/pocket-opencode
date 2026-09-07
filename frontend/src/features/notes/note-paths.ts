export type NoteFileKind = 'body' | 'audio' | 'image' | 'video' | 'file'

export function noteIdPrefix(noteId: string): string {
  const raw = (noteId || '').replace(/[^a-zA-Z0-9]/g, '')
  if (raw.length >= 2) return raw.slice(0, 2).toLowerCase()
  if (raw.length === 1) return `${raw.toLowerCase()}_`
  return '__'
}

export function noteDir(noteId: string, createdAt: number): string {
  const d = new Date(createdAt)
  const yyyy = String(d.getUTCFullYear())
  const mm = String(d.getUTCMonth() + 1).padStart(2, '0')
  const dd = String(d.getUTCDate()).padStart(2, '0')
  return `notes/${yyyy}/${mm}/${dd}/${noteIdPrefix(noteId)}/${noteId}`
}

export function noteFileRelPath(
  dir: string,
  kind: NoteFileKind,
  index = 1,
  ext = 'bin',
): string {
  if (kind === 'body') return `${dir}/body.md`
  const folder = kind === 'image' ? 'images' : kind === 'video' ? 'videos' : kind === 'audio' ? 'audio' : 'files'
  const seq = String(index).padStart(2, '0')
  return `${dir}/${folder}/${seq}.${ext.replace(/^\./, '')}`
}
