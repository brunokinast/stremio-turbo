// TypeScript test subjects go through tests/tsTransform.js; JavaScript keeps
// Jest's default transform.
module.exports = {
    transform: {
        '\.tsx?$': '<rootDir>/tests/tsTransform.js',
        '\.jsx?$': 'babel-jest',
    },
};
