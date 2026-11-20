// Jest transform for the fork's TypeScript modules: plain transpilation with
// the repo's own compiler (type checking is `tsc`'s job), to CommonJS.

const ts = require('typescript');

module.exports = {
    process(source, path) {
        const { outputText } = ts.transpileModule(source, {
            fileName: path,
            compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020, jsx: ts.JsxEmit.React, esModuleInterop: true },
        });
        return { code: outputText };
    },
};
