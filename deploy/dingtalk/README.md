# 钉钉卡片模板

本目录保存项目使用的钉钉卡片平台完整 JSON 导出文件，保留原始内容，仅改为固定文件名，便于导入和后续更新。

| 文件 | 用途 | 服务端环境变量 |
|---|---|---|
| [notice-card.json](notice-card.json) | 问题单与成员加入通知 | `COHIVE_DINGTALK_NOTICE_TEMPLATE` |
| [approval-card.json](approval-card.json) | 注册、加入、设备申请审批 | `COHIVE_DINGTALK_APPROVAL_TEMPLATE` |

## 导入与配置

在自己的钉钉企业应用下，分别新建通知模板和审批模板，通过卡片模板编辑器的 JSON 导入功能选择对应文件。保存、预览并发布后，把各自生成的 **cardTemplateId** 填入上表对应的环境变量，再按部署方式重新加载配置。

这两个文件是编辑器导出格式，包含 `editorData`、`widgetInfo`、`type` 和 `mode`；不是发送卡片 API 的请求体。Cohive 运行时使用模板编号，不会读取或自动上传这些 JSON。文件名、编辑器组件编号都不能当作模板编号。

完整的权限、Stream、环境变量配置、群绑定与成员连接步骤见 [钉钉接入指南](../../docs/guides/dingtalk.md)。

## 模板与服务端的约定

| 模板 | 变量 | 类型 | 说明 |
|---|---|---|---|
| 通知 | `title` | string | 通知标题 |
| 通知、审批 | `content` | markdown | 服务端生成的正文；保留 Markdown 组件以支持卡片内 @ 标签 |
| 审批 | `status` | string | 审批结果文字 |
| 审批 | `pending` | boolean | 为真时显示“批准”“拒绝”按钮；服务端按 API 参数格式传 `"true"` / `"false"` |
| 审批 | `tip` | string，私有 | 仅给点击者显示的反馈 |

审批按钮使用回传请求：批准回传 `{"decision":"approve"}`，拒绝回传 `{"decision":"reject"}`。不要修改这两个参数或变量名；回调由服务端通过 Stream 接收。

模板中的预览数据不会自动成为实际业务数据。导入后建议检查：

- 通知能正确展示 `title`、Markdown `content`；由另一个已绑定钉钉的成员接收通知，验证卡片内 @ 和提醒。自己发给自己时不会 @ 操作者本人。
- 审批预览中，`pending = true` 显示按钮，`pending = false` 隐藏按钮并显示 `status`；在真实群里验证批准、拒绝以及私有 `tip`。
- 固定头部“问题通知”“问题审批”和按钮“批准”“拒绝”是中文，不随工作空间语言改变；其他语言团队应在发布前修改这些文字。

更新模板时，从卡片平台重新导出完整 JSON，替换对应文件，并检查上述约定。重新发布后的模板编号需同步更新部署配置。
