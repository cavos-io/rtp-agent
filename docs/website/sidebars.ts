import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

// This runs in Node.js - Don't use client-side code here (browser APIs, JSX...)

/**
 * Creating a sidebar enables you to:
 - create an ordered group of docs
 - render a sidebar for each doc of that group
 - provide next/previous navigation

 The sidebars can be generated from the filesystem, or explicitly defined here.

 Create as many sidebars as you want.
 */
const sidebars: SidebarsConfig = {
  tutorialSidebar: [
    'introduction',
    {
      type: 'category',
      label: 'Get started',
      items: ['get-started/quickstart'],
    },
    {
      type: 'category',
      label: 'Build agents',
      items: ['build-agents/agents-and-sessions', 'build-agents/tools'],
    },
    {
      type: 'category',
      label: 'Multimodality',
      items: ['multimodality/overview'],
    },
    {
      type: 'category',
      label: 'Speech and audio',
      items: ['speech-audio/overview'],
    },
    {
      type: 'category',
      label: 'Logic and structure',
      items: ['logic-structure/overview'],
    },
    {
      type: 'category',
      label: 'Tools',
      items: ['tools/overview'],
    },
    {
      type: 'category',
      label: 'Turn detection and interruptions',
      items: ['turn-detection/overview'],
    },
    {
      type: 'category',
      label: 'Testing and evaluation',
      items: ['testing-evaluation/overview'],
    },
    {
      type: 'category',
      label: 'Agent server',
      items: ['agent-server/worker-lifecycle'],
    },
    {
      type: 'category',
      label: 'Models',
      items: [
        'models/overview',
        'models/llm',
        'models/stt',
        'models/tts',
        'models/realtime',
        'models/virtual-avatar',
      ],
    },
    {
      type: 'category',
      label: 'Manage and deploy',
      items: ['manage-deploy/configuration'],
    },
    {
      type: 'category',
      label: 'Reference',
      items: [
        'reference/packages',
        'reference/providers',
        'reference/configuration',
        'reference/parity',
        'reference/deferred',
      ],
    },
  ],
};

export default sidebars;
