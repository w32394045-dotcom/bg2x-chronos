/**
 * 颜色全部走 CSS 变量（定义见 src/style.css 的 [data-theme] 块），
 * 这样同一份静态类名可以同时服务 dark / light / claude 三套主题。
 *
 * 约定：`--c-*` 都是空格分隔的 RGB 三元组（如 `6 9 13`），
 * 配合 `<alpha-value>` 保留 Tailwind 的 `/50` 透明度修饰符能力。
 *
 * 注意：`ink / line / mute / brand / warn / danger / ok` 的 dark 取值与本文件
 * 改造前的硬编码十六进制完全一致；`amber / emerald / fuchsia / rose / sky`
 * 是组件里直接使用的状态色号，也按主题切换取值（dark 仍是 Tailwind 默认值），
 * 否则亮色主题下的 300 系文字（浅黄 / 浅粉）在白色面板上不可读。
 *
 * @type {import('tailwindcss').Config}
 */
const tone = (name) => `rgb(var(${name}) / <alpha-value>)`

module.exports = {
  content: ['./index.html', './src/**/*.{vue,ts}'],
  theme: {
    extend: {
      colors: {
        ink: {
          950: tone('--c-ink-950'),
          900: tone('--c-ink-900'),
          850: tone('--c-ink-850'),
          800: tone('--c-ink-800'),
          750: tone('--c-ink-750'),
          700: tone('--c-ink-700'),
          600: tone('--c-ink-600'),
          500: tone('--c-ink-500'),
        },
        line: {
          DEFAULT: tone('--c-line'),
          soft: tone('--c-line-soft'),
          strong: tone('--c-line-strong'),
        },
        mute: {
          DEFAULT: tone('--c-mute'),
          soft: tone('--c-mute-soft'),
          strong: tone('--c-mute-strong'),
        },
        brand: {
          50: tone('--c-brand-50'),
          200: tone('--c-brand-200'),
          300: tone('--c-brand-300'),
          400: tone('--c-brand-400'),
          500: tone('--c-brand-500'),
          600: tone('--c-brand-600'),
          700: tone('--c-brand-700'),
          900: tone('--c-brand-900'),
        },
        warn: {
          DEFAULT: tone('--c-warn'),
          soft: tone('--c-warn-soft'),
          deep: tone('--c-warn-deep'),
        },
        danger: {
          DEFAULT: tone('--c-danger'),
          soft: tone('--c-danger-soft'),
          deep: tone('--c-danger-deep'),
        },
        ok: {
          DEFAULT: tone('--c-ok'),
          soft: tone('--c-ok-soft'),
          deep: tone('--c-ok-deep'),
        },
        /**
         * 语义色：
         * - overlay：深色主题里是白色叠加层（hover / chip / ring），浅色主题换成深色叠加层；
         * - fg：最强的文字色（原 text-white）；
         * - scrim：模态遮罩底色（原 bg-ink-950/70 这类写死用法）；
         * - on-brand：品牌色上的文字（btn-primary 的 text-ink-950）。
         */
        overlay: tone('--c-overlay'),
        fg: tone('--c-fg'),
        scrim: tone('--c-scrim'),
        'on-brand': tone('--c-on-brand'),
        amber: {
          200: tone('--c-amber-200'),
          300: tone('--c-amber-300'),
          400: tone('--c-amber-400'),
          500: tone('--c-amber-500'),
        },
        emerald: {
          200: tone('--c-emerald-200'),
          300: tone('--c-emerald-300'),
          400: tone('--c-emerald-400'),
          500: tone('--c-emerald-500'),
        },
        fuchsia: {
          300: tone('--c-fuchsia-300'),
          400: tone('--c-fuchsia-400'),
          500: tone('--c-fuchsia-500'),
        },
        rose: {
          200: tone('--c-rose-200'),
          300: tone('--c-rose-300'),
          400: tone('--c-rose-400'),
          500: tone('--c-rose-500'),
        },
        sky: {
          200: tone('--c-sky-200'),
          300: tone('--c-sky-300'),
          400: tone('--c-sky-400'),
          500: tone('--c-sky-500'),
        },
      },
      fontFamily: {
        sans: [
          'system-ui',
          '-apple-system',
          'Segoe UI',
          'Microsoft YaHei',
          'PingFang SC',
          'Hiragino Sans GB',
          'Noto Sans CJK SC',
          'sans-serif',
        ],
        mono: ['ui-monospace', 'Cascadia Mono', 'Consolas', 'Menlo', 'monospace'],
      },
      boxShadow: {
        // 只保留变量间接层：dark 的取值与改造前逐字节一致。
        panel: 'var(--shadow-panel)',
        glow: 'var(--shadow-glow)',
        // CompareSlider 分割线的发光（原为写死的 rgba(108,235,219,.55)）
        compare: '0 0 12px var(--c-brand-glow)',
      },
      keyframes: {
        'fade-in': {
          from: { opacity: '0', transform: 'translateY(4px)' },
          to: { opacity: '1', transform: 'translateY(0)' },
        },
        'toast-in': {
          from: { opacity: '0', transform: 'translateX(12px) scale(0.98)' },
          to: { opacity: '1', transform: 'translateX(0) scale(1)' },
        },
        shimmer: {
          '0%': { backgroundPosition: '-200% 0' },
          '100%': { backgroundPosition: '200% 0' },
        },
        pulseSoft: {
          '0%, 100%': { opacity: '1' },
          '50%': { opacity: '0.35' },
        },
      },
      animation: {
        'fade-in': 'fade-in 140ms ease-out',
        'toast-in': 'toast-in 160ms ease-out',
        shimmer: 'shimmer 1.8s linear infinite',
        'pulse-soft': 'pulseSoft 1.6s ease-in-out infinite',
      },
    },
  },
  plugins: [],
}
