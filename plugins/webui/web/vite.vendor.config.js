import { defineConfig } from "vite";
import { resolve } from "node:path";

export default defineConfig({
  root: import.meta.dirname,
  mode: "production",
  define: {
    "process.env.NODE_ENV": JSON.stringify("production")
  },
  build: {
    outDir: "dist/vendor",
    emptyOutDir: false,
    target: "es2022",
    minify: true,
    sourcemap: true,
    lib: {
      entry: resolve(import.meta.dirname, "src/vendor/runtime.js"),
      formats: ["es"],
      fileName: () => "runtime.js"
    },
    rollupOptions: {
      output: {
        assetFileNames: "assets/[name]-[hash][extname]"
      }
    }
  }
});
