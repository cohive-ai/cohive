// 以英文语言包为类型来源：t("拼错的键") 在 pnpm typecheck 时报错。
import "i18next";
import type common from "../locales/en/common.json";
import type issues from "../locales/en/issues.json";
import type auth from "../locales/en/auth.json";
import type me from "../locales/en/me.json";
import type server from "../locales/en/server.json";
import type admin from "../locales/en/admin.json";

declare module "i18next" {
  interface CustomTypeOptions {
    defaultNS: "common";
    resources: {
      common: typeof common;
      issues: typeof issues;
      auth: typeof auth;
      me: typeof me;
      server: typeof server;
      admin: typeof admin;
    };
  }
}
