// Mantine theme: palettes, sizes and component looks.
//
// Colors and sizes repeat the tokens from styles.css: elements are styled
// through the theme instead of per-page style overrides.

import { createTheme, type MantineColorsTuple } from '@mantine/core'

/** Accent palette: light blue. Slots 1, 2, 4, 6 and 7 set how elements look. */
const brand: MantineColorsTuple = [
  '#eaf9ff',
  '#7dd3fc',
  '#5ec9f7',
  '#48c0f3',
  '#38bdf8',
  '#38bdf8',
  '#38bdf8',
  '#52c3f9',
  '#1588ba',
  '#0d6b94',
]

/** Success: running, confirmed. */
const success: MantineColorsTuple = [
  '#e6fffb',
  '#2dd4bf',
  '#2dd4bf',
  '#25c0ad',
  '#2dd4bf',
  '#25c0ad',
  '#2dd4bf',
  '#5ee0d0',
  '#0f9d8f',
  '#0b7a6f',
]

/** Warning: pending, being created. */
const warning: MantineColorsTuple = [
  '#fff4e0',
  '#f0a832',
  '#f0a832',
  '#e39c22',
  '#f0a832',
  '#e39c22',
  '#f0a832',
  '#ffc861',
  '#b45309',
  '#8a3f07',
]

/** Danger: removal, blocking, failure. */
const danger: MantineColorsTuple = [
  '#ffe9e7',
  '#f0645c',
  '#f0645c',
  '#e5534b',
  '#f0645c',
  '#e5534b',
  '#f0645c',
  '#ff7b73',
  '#c94a43',
  '#a33a34',
]

/** Neutral scale for dimmed labels and captions. */
const neutral: MantineColorsTuple = [
  '#e6edf3',
  '#97a5b1',
  '#7b8a97',
  '#64727e',
  '#8b99a5',
  '#5a6b7d',
  '#46525e',
  '#33404b',
  '#263340',
  '#1a232c',
]

/**
 * The dark scale sets the surfaces of every Mantine component:
 * dark-7 canvas and paper, dark-6 cards and fields, dark-5 hover,
 * dark-4 borders, dark-2 dimmed text, dark-0 body text.
 */
const dark: MantineColorsTuple = [
  '#e6edf3',
  '#c3ccd5',
  '#97a5b1',
  '#7b8a97',
  '#33455a',
  '#212c37',
  '#1a232c',
  '#1a232c',
  '#0f151b',
  '#0a0f14',
]

export const theme = createTheme({
  primaryColor: 'brand',
  primaryShade: { light: 8, dark: 6 },
  // Dark text on a light accent gives better contrast than white.
  autoContrast: true,
  black: '#0f151b',
  white: '#ffffff',

  colors: {
    brand,
    blue: brand,
    dark,
    gray: neutral,
    red: danger,
    green: success,
    teal: success,
    orange: warning,
    yellow: warning,
  },

  // Controls 6px, cards 8px, modals 10px.
  defaultRadius: 'sm',
  radius: { xs: '2px', sm: '6px', md: '8px', lg: '10px', xl: '16px' },

  fontFamily: 'Inter, "Segoe UI", system-ui, -apple-system, sans-serif',
  fontFamilyMonospace: '"JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace',
  // Body text is 14px.
  fontSizes: { xs: '12px', sm: '13px', md: '14px', lg: '16px', xl: '18px' },
  headings: {
    fontFamily: 'Inter, "Segoe UI", system-ui, -apple-system, sans-serif',
    fontWeight: '600',
    sizes: {
      h1: { fontSize: '26px', lineHeight: '1.25' },
      h2: { fontSize: '24px', lineHeight: '1.3' },
      h3: { fontSize: '22px', lineHeight: '1.3' },
      h4: { fontSize: '16px', lineHeight: '1.35' },
      h5: { fontSize: '14px', lineHeight: '1.4' },
      h6: { fontSize: '13px', lineHeight: '1.4' },
    },
  },

  components: {
    // Fields are clearly darker than cards, otherwise they blend into
    // the surrounding surface.
    //
    // The hint sits under the field, not above it: a field with a hint
    // would otherwise stand lower than its neighbour and the row of
    // fields would drift apart.
    Input: {
      defaultProps: { size: 'sm', inputWrapperOrder: ['label', 'input', 'description', 'error'] },
      styles: { input: { '--input-bg': 'var(--raised)' } },
    },
    Select: {
      defaultProps: { size: 'sm', inputWrapperOrder: ['label', 'input', 'description', 'error'] },
    },
    TextInput: {
      defaultProps: { size: 'sm', inputWrapperOrder: ['label', 'input', 'description', 'error'] },
    },
    PasswordInput: {
      defaultProps: { size: 'sm', inputWrapperOrder: ['label', 'input', 'description', 'error'] },
    },
    Textarea: {
      defaultProps: { size: 'sm', inputWrapperOrder: ['label', 'input', 'description', 'error'] },
    },
    NumberInput: {
      defaultProps: { size: 'sm', inputWrapperOrder: ['label', 'input', 'description', 'error'] },
    },
    // Small size: 28px by design rather than the 30px Mantine default.
    Button: {
      defaultProps: { size: 'sm' },
      vars: () => ({
        root: {
          '--button-height-xs': '28px',
          '--button-padding-x-xs': '10px',
          '--button-fz-xs': '12px',
        },
      }),
    },
    // Badges default to soft: translucent background and colored text.
    Badge: {
      defaultProps: { radius: 'xl', variant: 'light' },
      vars: () => ({
        root: { '--badge-height-xs': '20px', '--badge-padding-x-xs': '10px' },
      }),
    },
    Alert: { defaultProps: { variant: 'light' } },
    Modal: {
      defaultProps: { radius: 'lg', centered: true },
      styles: { content: { backgroundColor: 'var(--surface)' } },
    },
    Tooltip: { defaultProps: { withArrow: true } },
    // Cards and panels are rounded by 8px wherever they are used.
    Paper: { defaultProps: { radius: 'md' } },
    Card: { defaultProps: { radius: 'md' } },
    Table: {
      styles: {
        table: {
          '--table-border-color': 'var(--border)',
          '--table-hover-color': 'var(--hover)',
        },
      },
    },
  },
})
