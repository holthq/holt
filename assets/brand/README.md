# Holt 品牌素材

Holt 是 macOS 上的下一代 IDE：多个仓库同时摆在一个工作台里——文件树、编辑器、按仓库的 Git 审阅和终端都在一个窗口。

图标画的是 Holt 的本义，一片小树林的黄昏：三棵冷杉站在坡上，地平线上压着一轮琥珀色的落日，深青到暖橙的天。
没有分支、没有代码符号，只有「一个安静的地方」，用纸纹和柔和的渐变把科技感压下去。

## 文件

每个图两版：`original/` 是原图，`1mb/` 是压到 1 MiB 以内、直接上传用的版本。下面的路径都相对这个目录。

| 文件 | 尺寸 | 用途 |
|---|---|---|
| `original/icon-1024.png` | 1024 × 1024 | 应用图标（macOS 网格：主体 824，圆角 185.4） |
| `1mb/icon-1024.png` | 1024 × 1024 | 同上，829 KB |
| `original/avatar-1024.png` | 1024 × 1024 | 组织头像原图（GitHub 会裁成圆） |
| `1mb/avatar-512.png` | 512 × 512 | 组织头像上传版，310 KB |
| `original/social-2560x1280.png` | 2560 × 1280 | 仓库社交卡原图（按 2 倍渲染） |
| `1mb/social-1280x640.png` | 1280 × 640 | 仓库社交卡上传版，488 KB |

放进 GitHub 的位置：

- **组织头像**：`holthq` → Settings → Profile → Avatar 用 `1mb/avatar-512.png`（≤ 1 MiB，512 起）。
- **仓库社交预览**：`holt` → Settings → Social preview 用 `1mb/social-1280x640.png`（1280 × 640 或 2:1，≤ 1 MiB）。
  也适合当 README 头图。
- **应用图标**：`1mb/icon-1024.png` 换成 `.app` 的 `AppIcon`；从 1024 生成各尺寸的 `.icns`。

## 色板

| 颜色 | 值 | 用在哪 |
|---|---|---|
| 深青 | `#0C3B3E` | 天空顶部、深色背景 |
| 林绿 | `#285545` | 天空到地平线的过渡 |
| 夜绿 | `#0A2426` | 卡片左栏、深色 UI |
| 落日橙 | `#F0A64A` ~ `#B8744F` | 太阳、强调色 |
| 琥珀 | `#EBA750` | 小标题旁的短线、高亮 |
| 象牙 | `#F5ECDD` | 标题、正文反白 |
| 雾灰 | `#DCCDBB` | 次级文字 |

字体：标题用 **Avenir Next Demi Bold**（紧凑、略收字距），中文用 **苹方**；代码、版本号这类用 **JetBrains Mono**。

## 重新生成

```sh
./assets/brand/src/make.sh   # 需要 ImageMagick 和 rsvg-convert：brew install imagemagick librsvg
```

`src/art.png` 是 AI 生成的底图（1254 × 1254），脚本负责裁切、圆角、内侧微光、社交卡的排版，
再压两套版本；`1mb/` 里每个文件超过 1 MiB 会直接报错。
