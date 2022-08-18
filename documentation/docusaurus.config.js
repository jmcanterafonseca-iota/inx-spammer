const path = require('path');

module.exports = {
    plugins: [
        [
            '@docusaurus/plugin-content-docs',
            {
                id: 'inx-spammer-develop',
                path: path.resolve(__dirname, 'docs'),
                routeBasePath: 'inx-spammer',
                sidebarPath: path.resolve(__dirname, 'sidebars.js'),
                editUrl: 'https://github.com/iotaledger/inx-spammer/edit/develop/documentation',
                versions: {
                    current: {
                        label: 'Develop',
                        path: 'develop',
                        badge: true
                    },
                },
            }
        ],
    ],
    staticDirectories: [path.resolve(__dirname, 'static')],
};
