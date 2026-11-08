/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ['./index.html', './src/**/*.{vue,ts}'],
  theme: {
    extend: {
      colors: {
        ink: {
          950: '#06090d',
          900: '#0a0f15',
          850: '#0e141c',
          800: '#121a24',
          750: '#16212d',
          700: '#1b2836',
          600: '#22303f',
          500: '#2b3b4d',
        },
        line: {
          DEFAULT: '#22303f',
          soft: '#1a2532',
          strong: '#31465c',
        },
        mute: {
          DEFAULT: '#8b9bb0',
          soft: '#6b7c92',
          strong: '#b6c4d4',
        },
        brand: {
          50: '#e9fffa',
          200: '#9df5e6',
          300: '#6cebdb',
          400: '#3fded0',
          500: '#22c8bd',
          600: '#12a5a0',
          700: '#0d7f80',
          900: '#083b41',
        },
        warn: {
          DEFAULT: '#f4b740',
          soft: '#f9d488',
          deep: '#7a5410',
        },
        danger: {
          DEFAULT: '#f4676b',
          soft: '#f7a4a6',
          deep: '#7a2427',
        },
        ok: {
          DEFAULT: '#4ade80',
          soft: '#8ef0ae',
          deep: '#14532d',
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
        panel: '0 1px 0 0 rgba(255,255,255,0.03) inset, 0 12px 32px -18px rgba(0,0,0,0.9)',
        glow: '0 0 0 1px rgba(34,200,189,0.35), 0 12px 32px -12px rgba(34,200,189,0.25)',
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
