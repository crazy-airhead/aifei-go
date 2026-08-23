import { defineConfig } from 'vitepress'
import { withMermaid } from 'vitepress-plugin-mermaid'

// 站点内容：docs/guide（模块指南）+ docs/arch（架构设计）。
// docs/issues 与 **/_*.md（如 guide/_STYLE.md）通过 srcExclude 排除，不发布到站点。
export default withMermaid(defineConfig({
  lang: 'zh-CN',
  title: 'Aifei-Go',
  description:
    '轻量级 Go Web 框架 —— Just Service、零依赖核心、模块化设计（从 Java Aifei 移植）',
  base: '/aifei-go/',
  lastUpdated: true,
  sitemap: { hostname: 'https://crazy-airhead.github.io/aifei-go/' },
  srcExclude: ['**/issues/**', '**/_*.md'],
  // head 标签不会自动加 base 前缀，favicon 需写全路径
  head: [['link', { rel: 'icon', href: '/aifei-go/logo.svg' }]],
  themeConfig: {
    logo: '/logo.svg',
    nav: [
      { text: '首页', link: '/' },
      { text: '指南', link: '/guide/', activeMatch: '/guide/' },
      { text: '架构设计', link: '/arch/', activeMatch: '/arch/' },
    ],
    sidebar: {
      '/guide/': [
        {
          text: '开始',
          collapsed: false,
          items: [
            { text: '指南总览', link: '/guide/' },
            { text: '框架总览（HIO / Just Service）', link: '/guide/aifei-go' },
          ],
        },
        {
          text: '核心框架',
          collapsed: false,
          items: [
            { text: 'core — 核心包（路由 / Handler / Interceptor）', link: '/guide/core' },
          ],
        },
        {
          text: '核心库',
          collapsed: true,
          items: [
            { text: 'enjoy — 模板 / 表达式引擎', link: '/guide/enjoy' },
            { text: 'db — 数据库访问（Row / Dao / Enjoy SQL）', link: '/guide/db' },
            { text: 'json — JSON 工具', link: '/guide/json' },
            { text: 'log — 日志接口', link: '/guide/log' },
            { text: 'config — 分层配置', link: '/guide/config' },
          ],
        },
        {
          text: '运行时',
          collapsed: true,
          items: [
            { text: 'http — net/http 适配器', link: '/guide/http' },
            { text: 'server — 生产启动层', link: '/guide/server' },
            { text: 'server 定制 — 多模式响应 / JWT / 定制路由', link: '/guide/server-customization' },
          ],
        },
        {
          text: '独立框架',
          collapsed: true,
          items: [
            { text: 'nami — HTTP RPC 客户端', link: '/guide/nami' },
            { text: 'dami — 进程内事件总线', link: '/guide/dami' },
            { text: 'flow — 流程编排引擎', link: '/guide/flow' },
          ],
        },
        {
          text: '代码生成',
          collapsed: true,
          items: [
            { text: 'generator — Schema → CRUD 代码', link: '/guide/generator' },
            { text: 'damigen — dami 接口代码生成', link: '/guide/damigen' },
          ],
        },
        {
          text: '插件 · 中间件集成',
          collapsed: true,
          items: [
            { text: 'cache — 两级缓存', link: '/guide/cache' },
            { text: 'storage — 文件存储', link: '/guide/storage' },
            { text: 'kafka — Kafka 生产 / 消费', link: '/guide/kafka' },
            { text: 'nacos — 注册 / 配置 / 发现', link: '/guide/nacos' },
            { text: 'elasticsearch — ES 客户端', link: '/guide/elasticsearch' },
            { text: 'xxljob — 分布式任务调度', link: '/guide/xxljob' },
            { text: 'swagger — OpenAPI 文档', link: '/guide/swagger' },
          ],
        },
        {
          text: '插件 · 框架集成',
          collapsed: true,
          items: [
            { text: 'dami-plugin — dami 插件封装', link: '/guide/dami-plugin' },
            { text: 'data-isolate — 数据隔离', link: '/guide/data-isolate' },
            { text: 'flow-plugin — flow 组装插件', link: '/guide/flow-plugin' },
          ],
        },
      ],
      '/arch/': [
        {
          text: '总览',
          collapsed: false,
          items: [
            { text: '架构索引', link: '/arch/' },
            { text: '实施方案总览', link: '/arch/00-overview' },
            { text: 'Java → Go 对照', link: '/arch/java-go-comparison' },
            { text: 'Java v1.1.0 同步', link: '/arch/java-v1.1.0-sync' },
          ],
        },
        {
          text: '六阶段实施',
          collapsed: true,
          items: [
            { text: 'Phase 1 · 核心框架', link: '/arch/01-phase1-core' },
            { text: 'Phase 2 · Enjoy 引擎', link: '/arch/02-phase2-enjoy' },
            { text: 'Phase 3 · db 模块', link: '/arch/03-phase3-db' },
            { text: 'Phase 4 · 工具库', link: '/arch/04-phase4-utils' },
            { text: 'Phase 5 · 高级特性', link: '/arch/05-phase5-advanced' },
            { text: 'Phase 6 · 示例', link: '/arch/06-phase6-example' },
          ],
        },
        {
          text: '专项设计',
          collapsed: true,
          items: [
            { text: '多表关联映射', link: '/arch/multi-table-mapping' },
            { text: '数据隔离', link: '/arch/data-isolate' },
            { text: '日志插件', link: '/arch/log-plugin' },
            { text: '微服务规划', link: '/arch/microservice' },
            { text: '可观测性', link: '/arch/observability' },
          ],
        },
        {
          text: 'Dami 设计',
          collapsed: true,
          items: [
            { text: '01 · Go 生态对照', link: '/arch/dami/01-go-comparison' },
            { text: '02 · 迁移设计', link: '/arch/dami/02-migration-design' },
          ],
        },
        {
          text: 'Flow 设计',
          collapsed: true,
          items: [
            { text: '00 · 总览', link: '/arch/flow/00-overview' },
            { text: '01 · Go 对照', link: '/arch/flow/01-go-comparison' },
            { text: '02 · 核心设计', link: '/arch/flow/02-core-design' },
            { text: '03 · 配置与求值', link: '/arch/flow/03-config-and-eval' },
            { text: '04 · 工作流设计', link: '/arch/flow/04-workflow-design' },
            { text: '05 · TDD 计划', link: '/arch/flow/05-tdd-plan' },
            { text: '06 · MySQL 仓储', link: '/arch/flow/06-mysql-repository' },
          ],
        },
        {
          text: 'Nami 设计',
          collapsed: true,
          items: [
            { text: '01 · Java 对照', link: '/arch/nami/01-java-comparison' },
            { text: '02 · 契约设计', link: '/arch/nami/02-design' },
          ],
        },
      ],
    },
    socialLinks: [
      { icon: 'github', link: 'https://github.com/crazy-airhead/aifei-go' },
    ],
    editLink: {
      pattern: 'https://github.com/crazy-airhead/aifei-go/edit/master/docs/:path',
      text: '在 GitHub 上编辑此页',
    },
    search: {
      provider: 'local',
    },
    outline: { level: [2, 3], label: '本页目录' },
    docFooter: { prev: '上一篇', next: '下一篇' },
    lastUpdated: { text: '最后更新' },
    returnToTopLabel: '回到顶部',
    sidebarMenuLabel: '菜单',
    darkModeSwitchLabel: '主题',
    lightModeSwitchTitle: '切换到浅色',
    darkModeSwitchTitle: '切换到深色',
    footer: {
      message:
        '基于 <a href="https://github.com/crazy-airhead/aifei-go/blob/master/LICENSE" target="_blank">Apache-2.0</a> 许可发布',
      copyright: 'Copyright © 2026 crazy-airhead',
    },
  },
  // 文档中的 ```mermaid 代码块（ASCII 架构图逐步迁移为 Mermaid）
  mermaid: {
    theme: 'default',
  },
}))
