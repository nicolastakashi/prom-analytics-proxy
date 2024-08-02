/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      keyframes: {
        'fade-in': {
          '0%': { opacity: 0 },
          '100%': { opacity: 1 },
        },
        'fade-out': {
          '0%': { opacity: 1 },
          '100%': { opacity: 0 },
        },
      },
      animation: {
        'fade-in': 'fade-in 0.5s ease-out',
        'fade-out': 'fade-out 0.5s ease-out',
      },
      colors: {
        blumine: {
          '50': '#e7fffe',
          '100': '#c2fffd',
          '200': '#8cfffa',
          '300': '#3dfff7',
          '400': '#00fff6',
          '500': '#00eaff', // Primary color
          '600': '#00b8e3', // Slightly darker for header
          '700': '#0090b5',
          '800': '#007290',
          '900': '#005c78',
          '950': '#003d55',
        },
      }
    },
  },
  plugins: [],
}