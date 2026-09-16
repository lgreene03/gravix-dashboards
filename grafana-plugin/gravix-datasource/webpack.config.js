// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Grafana loads a plugin's frontend as an AMD module and provides @grafana/*
// and react itself at runtime. Bundling them would ship a second React into a
// page that already has one, which breaks hooks in ways that look like the
// plugin's fault. Hence libraryTarget: 'amd' and the externals list.

const path = require('path');
const CopyWebpackPlugin = require('copy-webpack-plugin');

module.exports = {
  target: 'web',
  context: path.join(__dirname, 'src'),
  entry: { module: './module.ts' },
  output: {
    filename: '[name].js',
    path: path.join(__dirname, 'dist'),
    libraryTarget: 'amd',
    publicPath: 'public/plugins/gravix-datasource/',
  },
  externals: [
    'react',
    'react-dom',
    '@grafana/data',
    '@grafana/ui',
    '@grafana/runtime',
    'lodash',
  ],
  resolve: {
    extensions: ['.ts', '.tsx', '.js'],
  },
  module: {
    rules: [
      {
        test: /\.tsx?$/,
        exclude: /node_modules/,
        use: {
          loader: 'ts-loader',
          options: { transpileOnly: false, configFile: path.join(__dirname, 'tsconfig.json') },
        },
      },
    ],
  },
  plugins: [
    // plugin.json has to sit beside module.js in dist/ or Grafana will not see
    // the plugin at all.
    new CopyWebpackPlugin({ patterns: [{ from: '../plugin.json', to: '.' }] }),
  ],
};
