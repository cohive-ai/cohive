// Trans 会先把插值填进译文再解析标签：全局关了转义，值里带 <b> 之类就会被当成组件标签。
// 带用户数据（名字、工作空间显示名等）的 Trans 都展开这个常量：值先转义，解析完再还原成纯文本。
export const safeTrans = { tOptions: { interpolation: { escapeValue: true } }, shouldUnescape: true } as const;
