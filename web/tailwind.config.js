/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        noir: { DEFAULT: '#0c0a09', 2: '#131110', 3: '#1b1816' },
        ink: { DEFAULT: '#ece4d6', soft: '#cfc6b6' },
        muted: { DEFAULT: '#8f8576', 2: '#6a6055' },
        gold: { DEFAULT: '#c6a35c', 2: '#e2c885', deep: '#9a7d40' },
      },
      fontFamily: {
        serif: ['"Bodoni Moda"', 'Georgia', 'serif'],
        sans: ['Jost', 'system-ui', 'sans-serif'],
      },
      letterSpacing: { kicker: '0.34em', wide2: '0.2em' },
      transitionTimingFunction: {
        lux: 'cubic-bezier(.22,.61,.36,1)',
        io: 'cubic-bezier(.76,0,.24,1)',
      },
    },
  },
  plugins: [],
}
