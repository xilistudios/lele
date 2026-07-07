import { defineConfig } from "@farmfe/core";
import postcss from "@farmfe/js-plugin-postcss";

export default defineConfig({
  compilation: {
    lazyCompilation: false,
    input: {
      index: "./index.html",
    },
    output: {
      path: "./dist",
    },
    define: {
      "import.meta.env.PROD": process.env.NODE_ENV === "production",
      "import.meta.env.DEV": process.env.NODE_ENV !== "production",
      "import.meta.env.VITE_LELE_API_URL": process.env.NODE_ENV === "production"
        ? (process.env.VITE_LELE_API_URL ? JSON.stringify(process.env.VITE_LELE_API_URL) : "undefined")
        : JSON.stringify(process.env.VITE_LELE_API_URL || "http://localhost:18793"),
    },
    sourcemap: false,
  },
  server: {
    port: 3005,
  },
  plugins: ["@farmfe/plugin-react", postcss()],
});
