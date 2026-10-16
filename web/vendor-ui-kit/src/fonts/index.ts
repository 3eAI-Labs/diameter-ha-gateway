// Inter, self-hosted — the canonical brand typeface for all 3eAI Labs UIs.
// Consumers MUST serve the woff2 files from their own origin: loading Inter
// from fonts.googleapis.com sends every visitor's IP to Google before consent
// (LG Muenchen I, 3 O 17493/20, Jan 2022 — self-hosting named as the remedy).
// One variable file per subset (font-weight 100–900); Google's four weight
// files are byte-identical, so shipping one per subset is lossless.
// Inter is under the SIL Open Font License 1.1: OFL.txt (the upstream
// LICENSE.txt, verbatim) goes wherever the woff2 files are copied.

export const INTER_FONT_FAMILY = 'Inter, system-ui, -apple-system, sans-serif'

interface InterSubset {
  file: string
  unicodeRange: string
}

// latin-ext before latin: the ranges overlap on U+0304, U+0308 and U+0329,
// and the later @font-face wins for overlaps.
const INTER_SUBSETS: InterSubset[] = [
  {
    file: 'inter-latin-ext.woff2',
    unicodeRange:
      'U+0100-02BA,U+02BD-02C5,U+02C7-02CC,U+02CE-02D7,U+02DD-02FF,U+0304,U+0308,U+0329,U+1D00-1DBF,U+1E00-1E9F,U+1EF2-1EFF,U+2020,U+20A0-20AB,U+20AD-20C0,U+2113,U+2C60-2C7F,U+A720-A7FF',
  },
  {
    file: 'inter-latin.woff2',
    unicodeRange:
      'U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD',
  },
]

export const INTER_FONT_FILES = INTER_SUBSETS.map((s) => s.file)

// For apps without a bundler (static HTML, SSR templates): copy the woff2
// files somewhere self-served, then inline the CSS this returns.
// dist/fonts/inter.css is generated from this function at build time with
// baseUrl '.' — bundler apps import that file directly.
export const interFontFaceCss = (fontsBaseUrl: string): string => {
  const base = fontsBaseUrl.replace(/\/+$/, '')
  return INTER_SUBSETS.map(
    ({ file, unicodeRange }) =>
      `@font-face{font-family:'Inter';font-style:normal;font-weight:100 900;font-display:swap;\n` +
      `  src:url('${base}/${file}') format('woff2');\n` +
      `  unicode-range:${unicodeRange};}`
  ).join('\n')
}
