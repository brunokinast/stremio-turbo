// TypeScript test subjects go through tests/tsTransform.js; JavaScript keeps
// Jest's default transform.
module.exports = {
    // The app's own import alias (webpack resolves it the same way).
    moduleNameMapper: {
        '^stremio/(.*)$': '<rootDir>/src/$1',
    },
    transform: {
        '\.tsx?$': '<rootDir>/tests/tsTransform.js',
        '\.jsx?$': 'babel-jest',
    },
};
