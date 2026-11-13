import fs from "node:fs";
import path from "node:path";
import VueI18nPlugin from "@intlify/unplugin-vue-i18n/vite";
import vue from "@vitejs/plugin-vue";
import { defineConfig, type Plugin } from "vite";
import checker from "vite-plugin-checker";
import { compression } from "vite-plugin-compression2";

const isDevBuild = process.env.DEV_BUILD === "true";
const backendWebDist = path.resolve(__dirname, "../backend/internal/web/dist");

// aceModules ships the Ace editor's language modes with the build so the
// editor loads them from this origin (see Editor.vue's basePath) instead of a
// public CDN. Only the mode files are copied; the themes and extensions the
// editor uses are imported statically. Emitted before the compression plugin
// runs so they are pre-gzipped like every other script.
function aceModules(): Plugin {
  const srcDir = path.resolve(__dirname, "node_modules/ace-builds/src-min-noconflict");
  return {
    name: "ace-modules",
    apply: "build",
    generateBundle() {
      for (const file of fs.readdirSync(srcDir)) {
        if (!/^mode-[\w-]+\.js$/.test(file)) continue;
        this.emitFile({
          type: "asset",
          fileName: `ace/${file}`,
          source: fs.readFileSync(path.join(srcDir, file)),
        });
      }
    },
  };
}

const plugins = [
  vue(),
  aceModules(),
  VueI18nPlugin({
    runtimeOnly: false,
    include: [path.resolve(__dirname, "./src/i18n/**/*.json")],
  }),
  // Only compress in production builds
  !isDevBuild && compression({
    include: /\.(js|woff2|woff)(\?|$)/i,
    deleteOriginalAssets: true,
  }),
  // Disable checker in watch mode to prevent task failures
  !isDevBuild && checker({
    typescript: false, // Disable redundant check
    vueTsc: {
      tsconfigPath: "./tsconfig.json",
    },
  }),
].filter(Boolean);

const resolve = {
  alias: {
    "@": path.resolve(__dirname, "src"),
  },
};

// https://vitejs.dev/config/
export default defineConfig(() => {
  return {
    plugins,
    resolve,
    base: "",
    define: {
      __VUE_I18N_LEGACY_API__: JSON.stringify(false),
      __VUE_I18N_FULL_INSTALL__: JSON.stringify(false),
    },
    build: {
      outDir: backendWebDist,
      emptyOutDir: true,
      // Optimize for watch mode stability
      watch: isDevBuild ? {
        // Add buildDelay to batch multiple changes
        buildDelay: 500,
      } : null,
      target: "es2022",
      sourcemap: false,
      chunkSizeWarningLimit: 5000,
      rollupOptions: {
        input: {
          index: path.resolve(__dirname, "./public/index.html"),
        },
        output: {
          manualChunks(id) {
            if (id.includes("/src/store/")) {
              return "store";
            }
            if (id.includes("node_modules/highlight.js")) {
              return "highlightjs";
            }
            if (id.includes("node_modules/mammoth")) {
              return "mammoth";
            }
            if (id.includes("node_modules/jszip")) {
              return "jszip";
            }
            if (id.includes("node_modules/epubjs")) {
              return "epubjs";
            }
            return undefined;
          },
        },
        // Better error handling in watch mode
        onwarn(warning, warn) {
          // Suppress certain warnings in dev mode
          if (isDevBuild && warning.code === "UNUSED_EXTERNAL_IMPORT") {
            return;
          }
          warn(warning);
        },
      },
    },
    experimental: {
      renderBuiltUrl(filename, { hostType }) {
        if (hostType === "js") {
          // Use relative paths instead of runtime function
          return { relative: true };
        } else if (hostType === "html") {
          return `{{ .htmlVars.staticURL }}/${filename}`;
        } else {
          return { relative: true };
        }
      },
    },
    test: {
      globals: true,
      include: ["src/**/*.test.js"],
      exclude: ["src/**/*.vue"],
      environment: "jsdom",
      setupFiles: "tests/mocks/setup.js",
    },
  };
});
