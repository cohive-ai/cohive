# Security policy

[中文说明见下方](#中文)

## Supported versions

Cohive is in its first phase. Security fixes go into the latest release only; please upgrade to it before reporting.

## Reporting a vulnerability

**Do not open a public issue for security problems.** Report them privately through GitHub: on the repository page, open **Security → Report a vulnerability** ([direct link](https://github.com/cohive-ai/cohive/security/advisories/new)).

Please include:

- what an attacker can do, and what they need first (an account, a token, network access, ...);
- steps or a script to reproduce it, and the Cohive version or commit;
- any idea you have for a fix.

What to expect:

- We acknowledge your report within 7 days. Cohive is maintained by a small team, so please allow time for a fix.
- We keep you updated, agree on a disclosure date with you, and credit you in the advisory unless you prefer not to be named.

Areas we care most about:

- authentication and permissions (web sessions, device pairing, tokens, admin actions);
- one member reading or changing another member's issues or workspaces;
- bypassing the secret scanning that runs before issue content is saved;
- chat notifications leaking issue content beyond the title and brief;
- coding tools being steered into changing someone else's work without the user's confirmation.

Please do not test against servers you do not own, and do not access other people's data.

## 中文

**安全问题不要开公开议题。** 请在仓库页面 **Security → Report a vulnerability** 私密提交（[直达链接](https://github.com/cohive-ai/cohive/security/advisories/new)），写明攻击者能做什么、需要什么前提、复现步骤与版本。我们会在 7 天内确认收到，修复后与你约定公开时间，并在安全公告里致谢（除非你不希望署名）。只支持最新发布版。请不要测试不属于你的服务器，也不要访问他人的数据。
