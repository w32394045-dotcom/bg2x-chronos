import { describe, expect, it } from 'vitest'
import { gradientDataUrl, toFileUrl, toImageSrc } from '../media'

describe('toFileUrl', () => {
  it('Windows 绝对路径转成合法的 file:/// URL，盘符冒号保留', () => {
    expect(toFileUrl('C:\\Pics\\a.jpg')).toBe('file:///C:/Pics/a.jpg')
    expect(toFileUrl('C:\\Users\\a b\\图 1.png')).toBe('file:///C:/Users/a%20b/%E5%9B%BE%201.png')
  })

  it('POSIX 路径加前导斜杠', () => {
    expect(toFileUrl('/home/user/a.png')).toBe('file:///home/user/a.png')
  })

  it('已经是 URL / data URL 时原样返回', () => {
    expect(toFileUrl('https://example.com/a.jpg')).toBe('https://example.com/a.jpg')
    expect(toFileUrl('data:image/png;base64,AAAA')).toBe('data:image/png;base64,AAAA')
  })
})

describe('gradientDataUrl / toImageSrc', () => {
  it('同 seed 同 variant 生成同一张图（确定性）', () => {
    expect(gradientDataUrl('C:\\a.jpg', 'orig')).toBe(gradientDataUrl('C:\\a.jpg', 'orig'))
    expect(gradientDataUrl('C:\\a.jpg', 'orig')).not.toBe(gradientDataUrl('C:\\a.jpg', 'out'))
  })

  it('浏览器（node 测试环境）下返回内联 SVG data URL', () => {
    const src = toImageSrc('C:\\Pics\\a.jpg', 'orig')
    expect(src.startsWith('data:image/svg+xml')).toBe(true)
    expect(src).not.toContain('file://')
  })

  it('空路径也返回可渲染的占位图而不是空字符串', () => {
    expect(toImageSrc('')).toMatch(/^data:image\/svg\+xml/)
  })
})
