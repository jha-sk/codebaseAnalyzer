// Baseline ESLint config, applied only to repos that have no config of their
// own — the JS/TS equivalent of the zero-config experience staticcheck and
// clippy already give Go and Rust. A repo with its own config gets that one;
// the analyser never overrides a team's chosen rules.
//
// ponytail: type-aware rules (typescript-eslint's no-floating-promises,
// no-misused-promises) are NOT enabled — they need a full type-check per
// lint, which roughly triples runtime and fails outright on repos whose
// tsconfig doesn't cover every file. Ceiling: some floating promises are
// caught only by promise/catch-or-return's syntactic approximation below.
// Upgrade path: switch to projectService when tsc already ran clean.
import js from "@eslint/js";
import tseslint from "typescript-eslint";
import promise from "eslint-plugin-promise";
import security from "eslint-plugin-security";
import globals from "globals";

const JS_FILES = ["**/*.js", "**/*.mjs", "**/*.cjs", "**/*.jsx"];
const TS_FILES = ["**/*.ts", "**/*.tsx", "**/*.mts", "**/*.cts"];

export default [
  {
    ignores: [
      "**/node_modules/**",
      "**/dist/**",
      "**/build/**",
      "**/.next/**",
      "**/out/**",
      "**/coverage/**",
    ],
  },
  { ...js.configs.recommended, files: [...JS_FILES, ...TS_FILES] },
  // typescript-eslint's recommended set is scoped to TS files only; applied
  // to plain .js it reports parse-level noise on valid JavaScript.
  ...tseslint.configs.recommended.map((c) => ({ ...c, files: TS_FILES })),
  {
    files: [...JS_FILES, ...TS_FILES],
    languageOptions: {
      globals: { ...globals.node, ...globals.browser },
    },
    plugins: { promise, security },
    rules: {
      "promise/catch-or-return": "error",
      "promise/no-return-wrap": "error",
      "promise/param-names": "error",
      "promise/no-new-statics": "error",
      "promise/valid-params": "error",
      "security/detect-child-process": "error",
      "security/detect-non-literal-fs-filename": "warn",
      "security/detect-eval-with-expression": "error",
      "security/detect-unsafe-regex": "error",
      "security/detect-buffer-noassert": "error",
      "security/detect-possible-timing-attacks": "warn",
      "security/detect-object-injection": "off", // notoriously noisy: fires on every obj[key]
    },
  },
];
