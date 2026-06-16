import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  tutorialSidebar: [
    'introduction',
    {
      type: 'category',
      label: 'Build Agents',
      items: [
        'build-agents/overview',
        {
          type: 'category',
          label: 'Get Started',
          items: [
            'build-agents/get-started/introduction',
            'build-agents/get-started/voice-ai-quickstart',
            'build-agents/get-started/agent-builder',
            'build-agents/get-started/agent-console',
            'build-agents/get-started/agent-embed-widget',
            'build-agents/get-started/prompting-guide',
          ],
        },
        {
          type: 'category',
          label: 'Multimodality',
          items: ['build-agents/multimodality/overview'],
        },
        {
          type: 'category',
          label: 'Speech & Audio',
          items: [
            'build-agents/speech-audio/text-transcriptions',
            'build-agents/speech-audio/modality-aware-instructions',
          ],
        },
        'build-agents/images-video/overview',
        {
          type: 'category',
          label: 'Logic & Structure',
          items: [
            'build-agents/logic-structure/overview',
            'build-agents/logic-structure/agent-sessions',
            'build-agents/logic-structure/chat-context',
            'build-agents/logic-structure/tasks-task-groups',
            'build-agents/logic-structure/workflows',
            'build-agents/logic-structure/supervisor-pattern',
          ],
        },
        {
          type: 'category',
          label: 'Tool Definition & Use',
          items: ['build-agents/tools/pipeline-nodes-hooks'],
        },
        {
          type: 'category',
          label: 'Turn Detection & Interruptions',
          items: [
            'build-agents/turn-detection-interruptions/agents-handoffs',
            'build-agents/turn-detection-interruptions/external-data-rag',
            'build-agents/turn-detection-interruptions/fallback-strategies',
          ],
        },
        {
          type: 'category',
          label: 'Testing & Evaluation',
          items: [
            'build-agents/testing-evaluation/overview',
            'build-agents/testing-evaluation/test-framework',
          ],
        },
        'build-agents/prebuilt/overview',
        {
          type: 'category',
          label: 'Agent Server',
          items: [
            'build-agents/agent-server/overview',
            'build-agents/agent-server/startup-modes',
            'build-agents/agent-server/server-lifecycle',
            'build-agents/agent-server/agent-dispatch',
            'build-agents/agent-server/job-lifecycle',
            'build-agents/agent-server/server-options',
          ],
        },
        {
          type: 'category',
          label: 'Models',
          items: [
            'build-agents/models/overview',
            'build-agents/models/pipeline-types',
            'build-agents/models/livekit-inference',
            'build-agents/models/llm',
            'build-agents/models/stt',
            'build-agents/models/tts',
            'build-agents/models/realtime',
            'build-agents/models/virtual-avatar',
          ],
        },
        {
          type: 'category',
          label: 'Providers',
          items: [
            'build-agents/providers/openai',
            'build-agents/providers/google',
            'build-agents/providers/azure',
            'build-agents/providers/aws',
            'build-agents/providers/xai',
            'build-agents/providers/groq',
            'build-agents/providers/cerebras',
            'build-agents/providers/other-implemented-providers',
          ],
        },
      ],
    },
    'agent-frontends/overview',
    'telephony/overview',
    'webrtc-transport/overview',
    {
      type: 'category',
      label: 'Manage & Deploy',
      items: ['manage-deploy/overview', 'manage-deploy/configuration'],
    },
    {
      type: 'category',
      label: 'Reference',
      items: [
        'reference/agents-framework',
        'reference/turn-handling-options',
        'reference/events-errors',
        'reference/dispatch-api',
        'reference/cli',
        'reference/model-parameters',
        'reference/packages',
        'reference/providers',
        'reference/configuration',
        'reference/parity',
        'reference/migration-matrix',
        'reference/deferred',
      ],
    },
  ],
};

export default sidebars;
