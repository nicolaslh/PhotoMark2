import type { Config } from 'tailwindcss'

export default {
  content: ['./index.html', './src/**/*.{vue,ts}'],
  theme: {
    extend: {
      fontFamily: {
        sans: ['Inter', 'ui-sans-serif', 'system-ui', 'sans-serif'],
      },
      colors: {
        ink: '#17202A',
        paper: '#F7F3EA',
        mint: '#4FB286',
        coral: '#D96C4F',
        steel: '#476A80',
      },
    },
  },
  plugins: [],
} satisfies Config
