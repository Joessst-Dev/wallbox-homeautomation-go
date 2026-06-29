import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'wha',
  description: 'PV-surplus EV charging on top of evcc — operator manual',
  base: '/wallbox-homeautomation-go/',
  lastUpdated: true,
  cleanUrls: true,

  themeConfig: {
    logo: { src: '/logo.svg', alt: 'wha' },

    nav: [
      { text: 'Guide', link: '/guide/introduction', activeMatch: '/guide/' },
      { text: 'Reference', link: '/reference/configuration', activeMatch: '/reference/' },
      {
        icon: 'github',
        link: 'https://github.com/Joessst-Dev/wallbox-homeautomation-go'
      }
    ],

    sidebar: {
      '/guide/': [
        {
          text: 'Guide',
          items: [
            { text: 'Introduction', link: '/guide/introduction' },
            { text: 'Hardware', link: '/guide/hardware' },
            { text: 'Quick Install', link: '/guide/quick-install' },
            { text: 'Manual Install', link: '/guide/manual-install' },
            { text: 'Configuration', link: '/guide/configuration' },
            { text: 'Dashboard', link: '/guide/dashboard' },
            { text: 'Updating', link: '/guide/updating' },
            { text: 'How It Works', link: '/guide/how-it-works' }
          ]
        }
      ],
      '/reference/': [
        {
          text: 'Reference',
          items: [
            { text: 'Configuration', link: '/reference/configuration' },
            { text: 'MQTT', link: '/reference/mqtt' },
            { text: 'Troubleshooting', link: '/reference/troubleshooting' },
            { text: 'FAQ', link: '/reference/faq' }
          ]
        }
      ]
    },

    search: {
      provider: 'local'
    },

    editLink: {
      pattern:
        'https://github.com/Joessst-Dev/wallbox-homeautomation-go/edit/main/docs/:path',
      text: 'Edit this page on GitHub'
    },

    socialLinks: [
      {
        icon: 'github',
        link: 'https://github.com/Joessst-Dev/wallbox-homeautomation-go'
      }
    ],

    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Copyright © 2024-present wha contributors'
    }
  },

  head: [['link', { rel: 'icon', href: '/wallbox-homeautomation-go/favicon.ico' }]]
})
