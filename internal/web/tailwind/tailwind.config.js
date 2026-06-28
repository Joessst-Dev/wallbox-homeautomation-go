/** @type {import('tailwindcss').Config} */
module.exports = {
  // Scan the embedded HTML templates so only used utilities are emitted.
  content: ["../templates/**/*.html"],
  // Honor the OS colour-scheme preference; no JS toggle needed.
  darkMode: "media",
  theme: {
    extend: {
      fontFamily: {
        sans: [
          "system-ui",
          "-apple-system",
          "Segoe UI",
          "Roboto",
          "Helvetica Neue",
          "Arial",
          "sans-serif",
        ],
      },
    },
  },
  plugins: [],
};
