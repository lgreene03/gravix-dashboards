/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  tutorialSidebar: [
    'getting-started',
    'roadmap',
    {
      type: 'category',
      label: 'Platform',
      items: [
        'architecture-overview',
        'deployment',
        'self-hosting',
      ],
    },
    {
      type: 'category',
      label: 'SDKs',
      items: [
        'sdk-go',
        'sdk-python',
        'sdk-node',
      ],
    },
    {
      type: 'category',
      label: 'Features',
      items: [
        'alerting',
        'billing-faq',
      ],
    },
    'api-reference',
    // Your data is yours, and these are the pages that prove it rather than
    // asserting it. Every one was orphaned from this sidebar until GRVX-1207;
    // a page nobody can navigate to is a page that does not exist.
    {
      type: 'category',
      label: 'Your data',
      items: [
        'bare-parquet-access',
        'migrating',
        // The exit path out of Gravix Cloud (GRVX-1403). Added here beyond that
        // spec's file list, because TestBoardIsInTheSidebar fails on any page in
        // docs-site/docs/ that cannot be navigated to — and an unreachable
        // migration guide is the one page that must never be hard to find.
        'cloud-to-selfhost-migration',
        'selfhost-to-cloud-migration',
        'leaving-gravix',
        'prove-it',
      ],
    },
    {
      type: 'category',
      label: 'Contributing',
      items: [
        'development-setup',
        'writing-a-plugin',
        'plugin-registry',
      ],
    },
    {
      type: 'category',
      label: 'Comparisons',
      items: [
        'vs-datadog',
        'vs-grafana-cloud',
        'vs-new-relic',
      ],
    },
  ],
};

module.exports = sidebars;
