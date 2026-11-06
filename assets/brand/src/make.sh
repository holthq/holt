#!/usr/bin/env bash
# 从 src/art.png 生成 original/ 和 1mb/ 两套图。
# 依赖 ImageMagick（magick）和 rsvg-convert（brew install imagemagick librsvg）。
set -euo pipefail
cd "$(dirname "$0")/.."

ART=src/art.png
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# ---- 应用图标：1024 画布，圆角方块（macOS 网格：主体 824，四周各 100）----
# 裁掉顶部空白，让太阳和三棵树占满方块；圆角 185.4 是 macOS 图标的标准半径。
magick "$ART" -crop 1000x1000+0+254 +repage -resize 824x824^ -gravity center -extent 824x824 "$TMP/body.png"
magick -size 824x824 xc:black -fill white -draw "roundrectangle 0,0 823,823 185.4,185.4" "$TMP/mask.png"
magick "$TMP/body.png" "$TMP/mask.png" -alpha off -compose CopyOpacity -composite "$TMP/body_r.png"
magick -size 1024x1024 xc:none "$TMP/body_r.png" -geometry +100+100 -compose Over -composite "$TMP/icon.png"
# 内侧 1.6px 的微光，深色桌面上也能看出边界
magick "$TMP/icon.png" -fill none -stroke 'rgba(255,255,255,0.16)' -strokewidth 1.6 \
  -draw "roundrectangle 101.2,101.2 922.8,922.8 184.2,184.2" original/icon-1024.png

# ---- 组织头像：整张画，GitHub 会裁成圆 ----
magick "$ART" -resize 1024x1024 original/avatar-1024.png

# ---- 仓库社交卡：1280x640 版式，按 2 倍渲染成原图 ----
# 左边是画面最左一列拉出来的渐变，和右边实拍画面无缝接上；接缝处用 240px 羽化。
magick "$ART" -crop 1x1254+0+0 +repage -resize '2560x1280!' -blur 0x10 "$TMP/panel.png"
magick "$ART" -resize 1280x1280 "$TMP/art.png"
magick -size 1280x1280 xc:black -fill white -draw "rectangle 240,0 1279,1279" -blur 0x55 "$TMP/feather.png"
magick "$TMP/art.png" "$TMP/feather.png" -alpha off -compose CopyOpacity -composite "$TMP/art_f.png"
magick "$TMP/panel.png" "$TMP/art_f.png" -geometry +1280+0 -compose Over -composite "$TMP/base.png"

cat > "$TMP/card.svg" <<'SVG'
<svg xmlns="http://www.w3.org/2000/svg" width="1280" height="640" viewBox="0 0 1280 640">
<image href="base.png" x="0" y="0" width="1280" height="640"/>
<g font-family="Avenir Next Demi Bold" fill="#F5ECDD">
<text x="92" y="312" font-size="138" letter-spacing="-3">Holt</text>
</g>
<rect x="96" y="348" width="76" height="6" rx="3" fill="#EBA750"/>
<text x="95" y="418" font-family="PingFang SC" font-size="32" fill="#F1E5D3">下一代 IDE，为多仓库而生</text>
<text x="95" y="468" font-family="PingFang SC" font-size="19" fill="#DCCDBB" opacity="0.72">文件树 · 编辑器 · Git 审阅 · 终端</text>
</svg>
SVG
rsvg-convert -w 2560 "$TMP/card.svg" -o original/social-2560x1280.png

# ---- 1mb：上传用的版本，全部压到 1 MiB 以内 ----
# 图标的纸纹让无损 PNG 卡在 1.05 MB 上下，0.5px 的模糊把噪声压掉，肉眼看不出差别
magick original/icon-1024.png -strip -blur 0x0.5 -define png:compression-level=9 1mb/icon-1024.png
magick original/avatar-1024.png -resize 512x512 -strip 1mb/avatar-512.png
magick original/social-2560x1280.png -resize 1280x640 -strip 1mb/social-1280x640.png

fail=0
for f in 1mb/*.png; do
  n=$(stat -f%z "$f")
  printf '%-28s %8d B  %s\n' "$f" "$n" "$([ "$n" -le 1048576 ] && echo OK || echo 超过 1 MiB)"
  [ "$n" -le 1048576 ] || fail=1
done
exit $fail
