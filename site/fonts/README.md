# Fonts

The site hosts its own fonts instead of loading them from Google Fonts, so it also works where Google's servers are blocked or slow. Both fonts are under the [SIL Open Font License 1.1](https://openfontlicense.org); each folder carries its license text. The files are the unmodified upstream builds.

| Font | Version | Files | Source | License |
|---|---|---|---|---|
| Michroma (Vernon Adams) | 1.100 | `michroma/Michroma-Regular.woff2` | [googlefonts/Michroma-font](https://github.com/googlefonts/Michroma-font) at commit `5822b9d1a733db7ae4f1a63a65a8b41b2d5b15cf`, `fonts/webfonts/Michroma-Regular.woff2` (the commit Google Fonts pins in `ofl/michroma/METADATA.pb`) | `michroma/OFL.txt` |
| JetBrains Mono (JetBrains) | 2.304 | `jetbrains-mono/JetBrainsMono-Regular.woff2`, `jetbrains-mono/JetBrainsMono-SemiBold.woff2` | [JetBrains/JetBrainsMono release v2.304](https://github.com/JetBrains/JetBrainsMono/releases/tag/v2.304), `JetBrainsMono-2.304.zip` (SHA-256 `6f6376c6ed2960ea8a963cd7387ec9d76e3f629125bc33d1fdcd7eb7012f7bbf`), `fonts/webfonts/` | `jetbrains-mono/OFL.txt` |

SHA-256 of the files here:

```
a84e40e4d0496a6c71d17e219e8065280374efa66ddfbb6b12363b6bbfdb1097  michroma/Michroma-Regular.woff2
a9cb1cd82332b23a47e3a1239d25d13c86d16c4220695e34b243effa999f45f2  jetbrains-mono/JetBrainsMono-Regular.woff2
918edad542a1da608fd2ba8daebaff9ac802309103fe760eed465b8b4e47faf1  jetbrains-mono/JetBrainsMono-SemiBold.woff2
```

`fonts.css` declares the faces; pages link it before their own styles. To update a font, replace the file with a newer upstream build, then update the version, source and checksum above.

Chinese headings use the system fonts (PingFang SC, Noto Sans SC, Microsoft YaHei); no Chinese font is shipped.
