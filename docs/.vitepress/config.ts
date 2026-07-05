import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'wha',
  description: 'PV-surplus EV charging on top of evcc — operator manual',
  base: '/wallbox-homeautomation-go/',
  lastUpdated: true,
  cleanUrls: true,

  // The legacy docs/install.md and docs/mqtt.md remain as GitHub-rendered
  // developer references (linked from README/CONTRIBUTING); their operator-facing
  // content is migrated into guide/ and reference/, so exclude the originals from
  // the published site to avoid duplicate pages and their repo-relative links.
  srcExclude: ['install.md', 'mqtt.md'],

  themeConfig: {
    logo: { src: '/logo.svg', alt: 'wha' },
    search: {
      provider: 'local'
    },
    socialLinks: [
      {
        icon: 'github',
        link: 'https://github.com/Joessst-Dev/wallbox-homeautomation-go'
      }
    ]
  },

  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/wallbox-homeautomation-go/favicon.svg' }]
  ],

  locales: {
    root: {
      label: 'English',
      lang: 'en',
      themeConfig: {
        nav: [
          { text: 'Guide', link: '/guide/introduction', activeMatch: '/guide/' },
          { text: 'Reference', link: '/reference/configuration', activeMatch: '/reference/' }
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
                { text: 'Secure Access', link: '/guide/secure-access' },
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
                { text: 'API', link: '/reference/api' },
                { text: 'MQTT', link: '/reference/mqtt' },
                { text: 'Troubleshooting', link: '/reference/troubleshooting' },
                { text: 'FAQ', link: '/reference/faq' }
              ]
            }
          ]
        },
        editLink: {
          pattern:
            'https://github.com/Joessst-Dev/wallbox-homeautomation-go/edit/main/docs/:path',
          text: 'Edit this page on GitHub'
        },
        footer: {
          message: 'Released under the MIT License.',
          copyright: 'Copyright © 2024-present wha contributors'
        }
      }
    },
    de: {
      label: 'Deutsch',
      lang: 'de',
      link: '/de/',
      themeConfig: {
        nav: [
          { text: 'Anleitung', link: '/de/guide/introduction', activeMatch: '/de/guide/' },
          { text: 'Referenz', link: '/de/reference/configuration', activeMatch: '/de/reference/' }
        ],
        sidebar: {
          '/de/guide/': [
            {
              text: 'Anleitung',
              items: [
                { text: 'Einführung', link: '/de/guide/introduction' },
                { text: 'Hardware', link: '/de/guide/hardware' },
                { text: 'Schnellinstallation', link: '/de/guide/quick-install' },
                { text: 'Manuelle Installation', link: '/de/guide/manual-install' },
                { text: 'Konfiguration', link: '/de/guide/configuration' },
                { text: 'Dashboard', link: '/de/guide/dashboard' },
                { text: 'Sicherer Zugriff', link: '/de/guide/secure-access' },
                { text: 'Aktualisieren', link: '/de/guide/updating' },
                { text: 'Funktionsweise', link: '/de/guide/how-it-works' }
              ]
            }
          ],
          '/de/reference/': [
            {
              text: 'Referenz',
              items: [
                { text: 'Konfiguration', link: '/de/reference/configuration' },
                { text: 'API', link: '/de/reference/api' },
                { text: 'MQTT', link: '/de/reference/mqtt' },
                { text: 'Fehlerbehebung', link: '/de/reference/troubleshooting' },
                { text: 'FAQ', link: '/de/reference/faq' }
              ]
            }
          ]
        },
        editLink: {
          pattern:
            'https://github.com/Joessst-Dev/wallbox-homeautomation-go/edit/main/docs/:path',
          text: 'Diese Seite auf GitHub bearbeiten'
        },
        footer: {
          message: 'Veröffentlicht unter der MIT-Lizenz.',
          copyright: 'Copyright © 2024-present wha contributors'
        }
      }
    }
  }
})
