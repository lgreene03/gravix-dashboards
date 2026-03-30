// @ts-check
// `@type` JSDoc annotations allow editor autocompletion and type checking
// (when paired with `@ts-check`). See: https://jsdoc.app/tags-type

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'Gravix Docs',
  tagline: 'Data-first HTTP observability',
  favicon: 'img/favicon.ico',

  url: 'https://docs.gravix.io',
  baseUrl: '/',

  organizationName: 'lgreene',
  projectName: 'gravix-dashboards',

  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'warn',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          sidebarPath: './sidebars.js',
          routeBasePath: '/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      }),
    ],
  ],

  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      navbar: {
        title: 'Gravix',
        items: [
          {
            type: 'docSidebar',
            sidebarId: 'tutorialSidebar',
            position: 'left',
            label: 'Docs',
          },
          {
            href: 'https://github.com/lgreene/gravix-dashboards',
            label: 'GitHub',
            position: 'right',
          },
        ],
      },
      footer: {
        style: 'dark',
        links: [
          {
            title: 'Docs',
            items: [
              { label: 'Getting Started', to: '/getting-started' },
              { label: 'Go SDK', to: '/sdk-go' },
              { label: 'Python SDK', to: '/sdk-python' },
              { label: 'Node SDK', to: '/sdk-node' },
            ],
          },
          {
            title: 'Compare',
            items: [
              { label: 'vs Datadog', to: '/vs-datadog' },
              { label: 'vs New Relic', to: '/vs-new-relic' },
              { label: 'vs Grafana Cloud', to: '/vs-grafana-cloud' },
            ],
          },
          {
            title: 'More',
            items: [
              { label: 'Alerting', to: '/alerting' },
              { label: 'Billing FAQ', to: '/billing-faq' },
              {
                label: 'GitHub',
                href: 'https://github.com/lgreene/gravix-dashboards',
              },
            ],
          },
        ],
        copyright: `Copyright © ${new Date().getFullYear()} Gravix. Built with Docusaurus.`,
      },
      prism: {
        additionalLanguages: ['go', 'python', 'bash', 'json'],
      },
      colorMode: {
        defaultMode: 'light',
        disableSwitch: false,
        respectPrefersColorScheme: true,
      },
    }),
};

export default config;
