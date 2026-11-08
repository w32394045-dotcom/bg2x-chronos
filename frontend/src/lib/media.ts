/**
 * 图片来源解析：桌面端用 file:// 绝对路径直接喂给 <img>，
 * 浏览器预览（mock）没有真实文件，用确定性生成的 SVG 渐变占位图代替。
 *
 * 只处理路径字符串，不读取任何字节（契约 §0 铁律 1）。
 */
import { isDesktop } from './env'

const ABSOLUTE_SCHEME = /^(data:|blob:|https?:|file:)/i

/** Windows / POSIX 路径 → 可直接放进 `src` 的 URL。 */
export function toFileUrl(path: string): string {
  const normalized = path.trim().replace(/\\/g, '/')
  if (!normalized) return ''
  if (ABSOLUTE_SCHEME.test(normalized)) return normalized
  const withRoot = normalized.startsWith('/') ? normalized : `/${normalized}`
  const encoded = withRoot
    .split('/')
    .map((segment, index) => {
      // 第一段为空（前导斜杠）；Windows 盘符形如 C: 需要保留冒号
      if (index === 0) return ''
      if (/^[A-Za-z]:$/.test(segment)) return segment
      return encodeURIComponent(segment)
    })
    .join('/')
  return `file://${encoded}`
}

function hashString(input: string): number {
  let hash = 2166136261
  for (let i = 0; i < input.length; i += 1) {
    hash ^= input.charCodeAt(i)
    hash = Math.imul(hash, 16777619)
  }
  return hash >>> 0
}

/**
 * 确定性渐变占位图（同路径同尺寸永远同图），用于浏览器预览与图片加载失败兜底。
 * `variant` 区分原图 / 超分结果，让对比滑块两侧肉眼可辨。
 */
export function gradientDataUrl(seed: string, variant: 'orig' | 'out' = 'orig', width = 720, height = 480): string {
  const hash = hashString(`${seed}|${variant}`)
  const hueA = hash % 360
  const hueB = (hueA + 55 + (hash >> 8) % 90) % 360
  const sat = variant === 'out' ? 62 : 38
  const light = variant === 'out' ? 46 : 26
  const blocks: string[] = []
  // 用几条斜向色带模拟画面结构，避免两张图完全一样
  for (let i = 0; i < 5; i += 1) {
    const y = Math.round((i + 1) * (height / 6))
    const w = 40 + ((hash >> (i * 3)) % 55)
    const h = 18 + ((hash >> (i * 2)) % 30)
    blocks.push(
      `<rect x="${Math.round((hash >> (i + 4)) % (width - w))}" y="${y}" width="${w}" height="${h}" rx="6" fill="hsl(${(hueA + i * 17) % 360} ${sat}% ${variant === 'out' ? 70 : 52}%)" opacity="0.5"/>`,
    )
  }
  const svg =
    `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}">` +
    `<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">` +
    `<stop offset="0" stop-color="hsl(${hueA} ${sat}% ${light}%)"/>` +
    `<stop offset="1" stop-color="hsl(${hueB} ${sat}% ${variant === 'out' ? 22 : 12}%)"/>` +
    `</linearGradient></defs>` +
    `<rect width="${width}" height="${height}" fill="url(#g)"/>` +
    `<circle cx="${width * 0.72}" cy="${height * 0.26}" r="${height * 0.13}" fill="hsl(${hueB} 90% 78%)" opacity="0.35"/>` +
    blocks.join('') +
    `</svg>`
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`
}

/** 路径 → `img.src`。桌面走 file://，浏览器预览走渐变占位图。 */
export function toImageSrc(path: string, variant: 'orig' | 'out' = 'orig'): string {
  if (!path) return gradientDataUrl('empty', variant)
  if (ABSOLUTE_SCHEME.test(path.trim())) return path.trim()
  if (isDesktop()) return toFileUrl(path)
  return gradientDataUrl(path, variant)
}
