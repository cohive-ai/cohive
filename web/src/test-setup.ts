// 现有测试断言中文文案：固定用中文跑，英文另有用例。
import { configure } from "@testing-library/react";
import i18n from "./i18n";

await i18n.changeLanguage("zh-CN");

// 机器负载高时（并行跑 Go 测试、变异验证）渲染与路由跳转会变慢，findBy / waitFor 默认 1 秒不够；统一放宽，条件满足会立刻返回，不拖慢正常运行。
configure({ asyncUtilTimeout: 5000 });
