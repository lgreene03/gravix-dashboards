/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  tutorialSidebar: [
    'getting-started',
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
