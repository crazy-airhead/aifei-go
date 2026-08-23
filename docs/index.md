---
layout: home

hero:
  name: Aifei-Go
  text: 轻量级 Go Web 框架
  tagline: Just Service · 零依赖核心 · 模块化设计 —— 从 Java Aifei 移植
  image:
    src: /logo.svg
    alt: Aifei-Go
  actions:
    - theme: brand
      text: 快速开始
      link: /guide/aifei-go
    - theme: alt
      text: 指南总览
      link: /guide/
    - theme: alt
      text: GitHub
      link: https://github.com/crazy-airhead/aifei-go

features:
  - icon: 🚀
    title: Just Service
    details: 方法名即路由：Register() 按命名约定（动词前缀 + 默认动作）自动映射 struct 方法为 RESTful 端点，无 Controller/Service/DAO 分层。
  - icon: 🧊
    title: 零外部依赖核心
    details: 核心库与独立框架（aifei/enjoy/db/json/log/nami/dami）仅用 Go 标准库；插件按需引入第三方库。
  - icon: 🧩
    title: 模块化设计
    details: Go workspace 多模块架构，各模块可独立 go get、按需组合，不拉入多余依赖。
  - icon: ✨
    title: Enjoy 模板引擎
    details: 自研模板语言（~2800 行）：表达式、条件、循环、宏定义、空安全（?? / ?.）、静态访问。
  - icon: 🗄️
    title: Active Record ORM
    details: Row + Dao 链式操作与变更追踪；Enjoy SQL 模板引擎（#where / #and + 18 种操作符，条件为空自动省略）。
  - icon: ⚙️
    title: 代码生成器
    details: 从数据库 Schema 自动生成类型安全的 CRUD 代码（base / model / dao / service，每表一个独立包）。
  - icon: 🌲
    title: 基数树路由 + AOP
    details: 每 HTTP 方法一棵 radix 树，支持参数与通配符；Handler 包装链 + 方法级 Interceptor 拦截器。
  - icon: 🔌
    title: 插件生态
    details: 两级缓存、Kafka、Nacos、S3 兼容存储、Elasticsearch、XXL-JOB、Swagger、数据隔离、流程编排等可选集成。
---
