import { defineConfig, globalIgnores } from 'eslint/config';
import nextVitals from 'eslint-config-next/core-web-vitals';
import nextTs from 'eslint-config-next/typescript';

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
    // app/theme/cosmo.* are emitted by `astryx theme build`; lint the source
  // (cosmoTheme.ts) instead of the generated artifacts.
  globalIgnores([
    '.next/**',
    'out/**',
    'build/**',
    'next-env.d.ts',
    'app/theme/cosmo.js',
    'app/theme/cosmo.d.ts',
    'app/theme/cosmo.variants.d.ts',
  ]),
]);

export default eslintConfig;
