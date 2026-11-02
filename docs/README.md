# 文档

开发约定和常用命令见仓库根目录的 [AGENTS.md](../AGENTS.md)。这里的文档补充具体说明：

- [architecture.md](architecture.md)：各个包的职责、状态流转和主要数据结构
- [testing.md](testing.md)：测试分工、验收标准对应的测试，以及内存测量方法
- [release.md](release.md)：版本号规则、构建、DMG 打包、签名、公证和 CI
- [shortcuts.md](shortcuts.md)：完整快捷键表，由命令注册表生成（`TestShortcutDoc -update`），请勿手动修改
- [mygo-pitfalls.md](mygo-pitfalls.md)：开发中遇到的问题和处理方法，按布局、列表、键盘、编辑器、终端、Git 等主题分节

`screenshots/` 存放 README 使用的截图，由 `TestScreenshot` 生成后选取并复制到这里。
