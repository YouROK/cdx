import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "node:path";

export default defineConfig({
  root: import.meta.dirname,
  base: "/ui/webui/",
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: false,
    target: "es2022",
    sourcemap: true,
    rollupOptions: {
      input: resolve(import.meta.dirname, "index.html"),
      external: ["react", "react-dom", "react-dom/client", "react/jsx-runtime"],
      output: {
        entryFileNames: "assets/app-[hash].js",
        chunkFileNames: "assets/[name]-[hash].js",
        assetFileNames: "assets/[name]-[hash][extname]"
      }
    }
  }
});
