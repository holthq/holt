# 踩过的坑

本文记录开发 Holt 时遇到的问题和处理约定。大部分与 mygo 的行为有关（以 go.mod 锁定的版本为准），也涉及 git、macOS 和 Go 标准库。
每条先说明现象或约定，再介绍处理方法。升级 mygo 后，部分条目可能不再适用，修改代码前需在新版本上确认。

新问题记入对应小节；已有同类记录时，直接更新原条目，避免重复。

## 布局与绘制

- `Grow(1)` 的 flex 基准是 0：带 Grow 的文字旁再放 `Spacer().Grow(1)`，两者平分空间，文字被省略号截断。需要文字占满空间时，只给文字设置 Grow。
- `TextArea` 要 `.MinHeight(0)`，否则会撑开面板。
- `PassThrough()` 只管这个元素自己，不管子元素：自己在 Overlay 里画的提示盖在别的元素上时，里面的 `Text` 也要 `PassThrough()`，
  不然按在提示下面那一行上的点击落到字上（文件树的行提示盖着下一行，⌘点下一行加选不上，见 `TestTreeRowTip`）。mygo 自己的 `Tooltip` 按下就收掉，碰不到这个。
- `wui.SplitPercent` 取 0–100，`wui.Split` / `SplitVertical` 取 DIP。
- `Row` 默认把子元素竖直居中。要子元素撑满行高（分隔条、整行可点的区域），给 Row 加 `.AlignItems(wui.Stretch)`。
- Row 里的子节点用了 `Fill()` 又没给宽度，会超出行宽，导致元素移出窗口、无法点击。侧栏这类定宽的用 `Width(w).Shrink(0)`。
- Column 中还有其他子元素时，不要用 `Fill()`：它占父容器宽高的 100%，加上其他子元素后会超出可用高度，底部内容被压到状态栏下面（Git 栏的提交框、
  文件页的筛选框都曾因此被遮挡）。用 `Grow(1).MinWidth(0).MinHeight(0)`，由 `TestSidebarFits` 检查。
- 滚动容器的子节点默认会缩。想让它比外面高（比如把横向滚动条挤到裁掉的地方），要 `Shrink(0)`。
- 同一行中有多个可收缩的片段（如面包屑）：
  - 不作处理时，所有片段会一起收缩成省略号。带 `Ellipsis` 的文字只要被缩了零点几个 DIP 就出"…"。
  - 全都 `Shrink(0)` 又会溢出、画到旁边的元素上。
  - 需要根据可用宽度自行计算（`crumbFit`：`c.MeasureText` 量文字，`Element.LayoutBounds()` 拿上一帧的宽，`c.Size()` 是这一帧的窗口大小），
    用 `Shrink(0)` 阻止片段收缩，用 `MaxWidth` 限制宽度，再用 `ClipX` 裁剪溢出内容。
- `LayoutBounds` / `Bounds` 是上一帧的，新元素的边界为空。这时用 `c.AnimationFrame()` 请求下一帧（`Context` 上没有 `Invalidate`）。
- 滚动条宽度是主题的 `ScrollbarWidth`，整个窗口一份，不能按元素改；横向滚动条（6 pt 宽，轨道 12 pt 高）画在滚动容器的底边里。
  标签条的处理方式：滚动容器比外面的 `Clip` 框高 `stripHidden`、底下 `Padding` 同样多，滚动条落在裁掉的那截里；
  再自己按 `ScrollState`（`TrackScroll`，`s.Bounds(c)` 是视口）画一条 2 pt 的细条。
- 列表选中行的颜色：
  - 选中行里的输入框（文件树的新建 / 重命名）要显式 `.TextColor(t.Text)`，否则会继承选中行的颜色，导致文字难以辨认。
  - 默认的选中样式是淡底，字不换色；只有设置里开了实心选中（`SolidSelection`）才是强调色底。判断"在强调色上"
    （字、图标换 `AccentText`）一律用 `onAccent(a, row.Selected())`，别直接用 `row.Selected()`。
    行里强调色的文字按钮用 `textButtonOn(…, onAccent(a, sel), …)`，否则实心选中时文字会与背景同色。
  - 测试里要量"选中变 `AccentText`"的，先 `a.St.Settings.SolidSelection = true`。
- `List` 选中行的圆角取主题的 `Radius`，没有单独的参数：侧栏的列表（圆角 5）在改了 `Radius` 的主题副本下建（`sideList`）。
- `c.SetTheme` 会重设根节点的底色和文字样式。卡片里用 `withTheme` 换主题副本之后，根节点的背景色会被重置。所以：
  - `root()` 在最后才按 `c.Vibrancy()` 设根的底色（透明 / `windowBg`）。
  - 列表的行可能在布局时才建（`root()` 已经跑完），行里的 `withTheme` 照样会改掉根的底色。`listLook.row` 建完一行就
    `c.Root().Background(l.bg)` 恢复背景色，否则柔和 / 纯色窗口底会被主题背景色替换。
  - 新列表沿用此写法：`l := newListLook(a, c, radius)`，`l.build(c, func(){ … wui.List(…).Rows(l.rows(fn)) })`。
  - 强调色的默认值是 `workspace.DefaultAccent`；`applyAccent` 要在每个窗口的根函数开头调（主窗口、设置、关于）。
- 每帧给卡片建一份主题副本，指针每帧都是新的。编辑器（`DefaultTheme(t)` 按值取色）和终端（只看 `Dark`）不会因此每帧重建；
  后续组件若按主题指针缓存数据，需留意这一点。
- 减少视觉装饰（见 `ui/accent.go` 开头的说明）：强调色只给"当前 / 焦点 / 可以放"和按类别分的色板。新加的角标、胶囊、横幅、文字按钮用
  `quietFill` / `bannerFill` 和正文色 / 次级色。
- mygo 的 `Split` 分隔线固定 1 px、不能改宽。面板之间的缝是自己的拖条（`resizer`、分屏的 `splitDivider`），悬停时画 `gapLine`。
- `Border` 画在元素里面；`Clip` 的元素的描边盖在子节点上面（卡片的描边不会被编辑器盖住）；`Shadow` 只画在外面，缝只有 6 pt，blur 不宜过大。
- 大片的模糊色斑用 `Painter.Shadow` 画，别用 `Element.Blur` 或 `Canvas`：
  - `Element.Blur` 是整层离屏位图，全窗口几 MB。
  - `Shadow(r, …)` 平时只画在 r 外面，但 r 宽或高为 0 时整块模糊的形状都画出来。给一条线段（宽 w−h、高 0）加 spread = h/2 + 余量
    就是一团圆角椭圆，sigma 是 blur 的一半。
  - 根节点可以 `Draw`（在背景之后、子节点之前，`Draw(nil)` 清掉）。盖在窗口底上的不透明元素（Git 栏贴顶的段头）要 `underBackdrop`
    再画一份同样的色斑、`Clip` 到自己，坐标用整个窗口的（滚动列表里 `Draw` 拿到的也是窗口坐标）。`TestBackdrop` 量接缝。
- 标题栏和红绿灯：
  - `TrafficLightPosition` 是整数 Point（关闭按钮的左上角），红绿灯 14 pt 高：标题栏高度取偶数（38）红绿灯才能正好居中。
  - `TitleBarHidden` 时 mygo 的 `c.TitleBar()` 报的是高 38、左边留 72（`TitleBarHiddenInset` 是 66 × 79）。
  - 文字在 Row 里竖直居中时，墨迹中心比图标低 0.5–0.75 pt。标题栏、标签条这类和图标同一行的字用 `textNudge` 往上挪（`TestTitleBarAlign` 检查误差是否在 ±0.5 pt 内）。
- `Tooltip` 默认在元素上面，只要上面放得下就往上，所以紧贴标题栏下面的元素的提示会盖住红绿灯。用 `tooltip(c, el, s)`：
  上一帧 `Bounds().Y` 不到 `tipBelowY` 就用 `TooltipBase` 往下弹，样子和 `Tooltip` 一样。
- 画虚线用 `Path.Dash(0, 段长)`。`Path` 的线帽默认是圆的，1.5 宽的线两头各多 0.75，2 的空隙只剩 0.5：配 `Cap(wui.ButtCap)`。
- mygo 测量文字宽度时不计末尾空格：`Text(c, " / ")` 画出来是"a /b"。两边要留空就用 `"/"` 加 Row 的 `Gap`（或 `PaddingX`），别靠文字里的空格。
- `wui.Color.Alpha` 会乘以原 alpha，而非直接设置：`t.Text.Alpha(0.62).Alpha(0.16)` 的 alpha 是 0.1。底块颜色从图形颜色算时，图形本身带 alpha 的话底块跟着更淡。
- 界面缩放是按窗口的：`c.SetZoom` 只作用于所在窗口，所以工作台的 `root`、`SettingsView`、`AboutView` 每帧各自 `c.SetZoom(a.ZoomFactor())`。
  新开一种窗口也要加这一句，否则新窗口会保持 100% 缩放。设置窗口、关于窗口不在工作台的帧里，设置一变要在 main 里 `Invalidate` 它们才重画。
  终端视图会先接键（它自带的 ⇧⌘= 改终端字号），`termKey` 里 `termZoomKeyFor` 把这几个键截下来交给界面缩放。
- `Painter.Scale()` 不含界面缩放。要按屏幕像素出位图（`ui/mdrender.go`），倍数是 `p.Scale() * a.ZoomFactor()`；只乘其中一项时，按 ⌘= 后图像会模糊，
  或在 Retina 屏幕上清晰度不足。首次绘制前尚不确定所在屏幕，暂用 2；绘制时由 `SeenScale` 记录实际值。
- 对话框只用 `dialogFrame`（`ui/dialogframe.go`），别用 `wui.AlertDialog` / `wui.Modal`：
  - 按钮用 `dlgButton`（普通 / 主 / 危险三种一样高），字段用 `dlgField` + `dlgInput`，命令预览用 `dlgCommand`。
  - 丢数据、改远程、改历史的确认用 `a.AskDanger`（红的），调用位置由 `app/danger_test.go` 扫描源码检查，新加一处要把函数名加进去；别的确认用 `a.Ask`。
  - 底部那行按钮先建（`Reverse()`）：提示里的"⏎ 删除分支"和按钮同字，按名字找（`Find` / `mustClick`）要先找到按钮。

## 列表、点击和拖动

- 列表上 `TrackScroll` 的 `ScrollState.ScrollTo` 滑动期间，`ListState.ScrollTo` / `ScrollIntoView` / `ScrollToEnd` 不起作用：滑动每帧在
  `adoptScroll` 里把偏移写回滑动的路上，压过列表在布局时挪的。侧栏的滚轮滑动因此自己每帧写 `ScrollState.Y`（`app.SideScroll`）。
- 鼠标滚轮（非 precise）mygo 一行算 40 DIP，macOS 加速后一次事件好几行，缓动前重后轻（40 的一格第一帧走 11.5，400 走 115），
  行高 24 的列表看着一顿一顿的；`Tester.Wheel` 发这种滚轮、`Tester.Scroll` 发触控板的。
- 元素的 `Bounds()` 是上一帧的布局；拿它当行的位置画叠在列表上的东西（吸顶栈）会比行晚一帧，要按列表两帧偏移之差挪一下。
- 输入回调里 `Context.Now()` 不是事件的时间，用 `InputEvent.Time`。
- `SetMenuPresentation(PopoverMenus)` 下 `ContextMenu` / `Menu` 的菜单每帧重建（系统菜单是打开时的快照）：开着时状态变了，项跟着变灰；
  测试里"选了打开时还能用、现在不能用的项"这种场景不再有，`Tester.Menu` / `ChooseMenuItem` 照样能用。
  Holt 画的菜单（`app.PopMenu`）也每帧重建：建菜单时按打开那一帧的下标取切片（搜索结果的 `s.Res.Matches[sr.Match]`），
  菜单开着时结果变少就越界。先核对还在、还是那一个，不在了什么也不加（菜单关掉）。
- 在树建完以后（`root` 的末尾）换掉列表的状态（换工作区）会让这一帧的布局在 `layoutList` 里空指针：动作要在建树之前做（Holt 菜单的选中项在 `root` 开头执行）。
- 行装了 `menuPress`（`HandleInput`）以后接输入的是行，不是列表的格子：`tt.Click(名字)` 报"不是输入目标"，用 `clickRow`。
- 双击：List 的行用 `RowDoubleClicked`，普通元素用 `DoubleClicked`。
- 行里的子元素一旦 `Clicked()` / `Pressed()` / `Drag`，会消耗该事件，列表无法再收到。不要为了获取修饰键而给行添加 `Clicked()`。
  文件树的做法：只让选中的行当拖动源（没选中的按下还是列表的拖选）；选中行的点击由 `TreeRowClicked` 记下，列表构建完成后，`ApplyTreeClick`
  按列表规则补充处理（⇧点、⌘点、双击）。大纲行的内容也 `Clicked()` 了，因此点击大纲行后焦点会移到编辑器；若要聚焦大纲，需点击列表下方的空白区域。
- List 对已经单独选中的行再点一次不发 `Changed`，带修饰键也一样。由此：
  - "再点一次回去"不能靠 `Changed`。工作树的行单击 / ↑↓ 是看历史（`viewWorktree`，不切回），回车 / `RowDoubleClicked` 是在工作区中打开；
    回去走菜单"查看分支历史"或胶囊的 ×（`ClearRefHistory` 顺带放开 `gitWt`）。
  - ⌥点文件行在侧边打开，是在 `Changed()` 那一帧看 `c.Modifiers()&wui.Alt`，所以 ⌥点一个已经只选着它的行什么都不做（用 ⌥回车 / 右键）。
    列表自己的回车提交是 `Shortcut(0, KeyEnter)`，会精确匹配修饰键，因此 ⌥回车不会被当作普通回车，要另挂 `FileList.OnShortcut(c, wui.Alt, wui.KeyEnter, …)`。
  - 比较段的标题行（`ui/history.go` 的 `compareRow`）自己用 `Clicked()` 切展开。双击、回车都会让列表报 `Submitted`，而双击的第一下已经切过一次，
    所以 `Submitted` 时鼠标在标题上（`compareRow` 返回 `Hovered()`）就不再切；代价是鼠标停在标题上按回车不切。
- 区分鼠标点击和键盘选择：行的 `Hovered()` 记下来，`Changed()` 时 `hover == sel` 就是鼠标点的。Git 栏每帧先把 `a.GitHover` 清成 -1 再画行。
- `ListState.Header` 的行贴在顶上，点击和方向键都不会选中它，要自己给底色（否则滚动时下方的行会透出来）。
  `ListState.ScrollTo(row, wui.Start)` 下一次排版时才滚。设了 `Key` 时行号变了，选中跟着 key 走。
- mygo 里已经选中的行自己数双击时，第二下按下报 `DoubleClicked()`，松开又报一次 `Clicked()`。`ApplyTreeClick` 记下双击的行和时间（`dbl` / `dblAt`），
  500 ms 内同一行紧跟的那次单击丢掉，否则双击目录会先展开再收起。
- mygo 的列表没有设置 ⇧ 选择锚点的接口。`ApplyTreeClick` 补的单击 / ⌘点、`chooseOnly`、`ClearTreeSel` 列表都不知道，它的锚点还在上一次它自己接的那行
  （可能已经收起看不见，⇧点就只选一行）。所以这几处记 `treeClick.anchor` / `own`，`list.Changed()` 时调 `TreeListChanged(mods)`：
  没按 ⇧ 的清掉 `own`，按着 ⇧ 且 `own` 就 `extendFrom` 按应用的锚点重选。其他地方也应通过 `chooseOnly` 修改选中状态。
- 文件树的列表开着 `MenuKeepsSelection`：不开时右键一行会先当成单击（换选中、去预览那一行）。多选菜单按 `a.MenuRows(row)` 决定作用于选中的全部还是只这一行；
  右键没选中的行由菜单回调里的 `a.MenuSelect(i)` 选中它（不预览）。
- Git 栏的光标 `GitSel` 和选中集合 `GitChosen` 必须同步更新。List 开着 `Selection = &a.GitChosen`，单击、方向键时 mygo 两个一起改；
  应用自己换行（跟着当前标签、跳过分组标题、Esc 收回多选）走 `a.chooseGit(rows, i)`。只改 `GitSel` 的话，高亮、回车开哪些 Diff（`ChosenChanges`）、
  右键算不算多选（`MenuChanges`）都还按旧行。测试里程序选行用 `pickGit(a, i)`。
- 列表上的 `OnShortcut` 会优先于行内输入框接收按键：键交给焦点链上最里面一个注册了这个键的元素，所以挂在列表上的 Esc、⌫ 会比提交框、搜索框自己的先拿到。
  列表级的键只在列表自己有焦点（`Focused(c)`，不是 `FocusWithin`）而且真用得上时才注册：Git 栏的 Esc 要 `GitChosen.Len() > 1`，搜索结果的 ⌘↩ / ⌫ 要 `s.List.Focused(c)`。
- List 行里的按钮或右键菜单改了行，要等列表建完再做。`rowButton` 在建的过程中按下就同步跑回调，而 mygo 的 `List` 接着还会拿开始建时的行数调
  `Key` / `Label`，若 `Rows` 已缩短，就会越界崩溃。搜索结果的"替换这一处" / "排除"交给 `do`，`sideList` 返回之后才跑；`Key` / `Label` 里也查一下下标。
  只开标签、复制这类不改行的动作不用等。
- 构建列表 / 菜单时不要修改仓库列表。菜单的构建函数和 `Drop` 都在建这一帧时跑，那时挪仓库、移除仓库会让列表拿着旧的行数接着建（Key 重复会崩）。
  记成 `a.MoveReq` / `a.RemoveReq`，`ui/view.go` 的 `pending()` 在这一帧画完后做。`TreeRows()` 是就地重建的：行的回调里别 `tree.ver++` 了再接着用手里的 rows。
- 同一个父元素下两个一样的 `c.Key` 会 panic（Tester 里也一样）。一项可能在好几个循环里各画一次时（设置搜索结果按页列），外层按页
  `wui.Column(c.Key(p))` 包一层、里面再 `c.Key(i)`。按下标画的表都照这个来。
- 元素不带 key 时按在父元素里的次序认。悬停才画的兄弟（Git 栏段头的"全部暂存"）插在按钮前面时，指针一移上去后面按钮的身份就变了，
  按下和松开不是同一个元素，`Clicked()` 不来。段头的拉取 / 推送按钮因此带 `c.Key("pull")` / `c.Key("push")`。
- 可点的父元素（Git 栏段头点了收起 / 展开）里的灰按钮，要是不调 `Clicked()`，这一下会落到父元素上。灰着也调 `Clicked()` 接住，只是不跑回调（`syncButton`）。
- 列表的 `Label` 既给键盘找字用，也是读屏念的名字。找字去掉开头的点（`Row.FindLabel`），VoiceOver 念的也就没有点；要两边不一样得等 mygo 把这两个分开。
  mygo 的键盘找字只认 A–Z、0–9、空格，中文名打不中，别在应用里绕。
- `repoMenu(a, m, r, tree)`：`tree` 为真时才有文件树专用的几项（收起其他仓库、展开有改动的目录、粘贴）；Git 栏、右栏历史的仓库菜单传 false。
- 应用内的值拖放（`wui.DragOver[T]` / `wui.Drop[T]`）：一个元素只有一个 `accepts`，后注册的类型会覆盖先注册的类型；命中链中，由最内层接受该值的元素处理。
  要同时接两种就套两层：仓库行外面包一层 `slot` Column 接 `RepoDrag`，里面那行接 `TreeDrag`；编辑器组的卡片外层接 `TreeDrag`、里面的内容区接 `*docs.Doc`。
  若内层也接收外层的类型，事件就不会再传到外层。
- 原生拖放（`DropData`）中，按格式接收的目标也会消耗文件拖放事件。命中链从里往外，匹配拖放板格式的目标会消耗整个拖放事件，不再退回 `DroppedFiles` / 窗口的 `OnFileDrop`。
  访达拖来的文件在拖放板上可能还带着文件名的文字，只接 `Text` 的目标会把它当成一段字。所以：
  - 编辑器和终端的 `DataDrop` 也接 `FileList`，包含文件时按文件处理（`OpenArgs` / `ShellFiles`）。
  - 值（`Drop[T]`）和格式（`DropData`）是两个字段，同一个元素两样都能登记。
  - `transfer.Drop` 没有落点，编辑器只能插在光标处（`ui/editor` 的 `offsetAt` 没导出）。
- mygo 的 `terminal.View` 自己登记了 `DroppedFiles`，按反斜杠转义、末尾不加空格贴进去。Holt 在格子外框登记格式目标（`DataDrop`，含 `FileList`）先接住，
  `ShellFiles` 贴单引号版，和从文件树拖行（`app.PastePaths`）一个写法。shell 已退出的分屏登记只接文件的 `ExitedDrop`（按窗口的规则打开），
  不登记的话访达的文件会落到视图自己的 `DroppedFiles`，写入没有 shell 的终端，无法显示结果。
- 拖动悬停触发展开时，需等所有行绘制完成：`DwellTree` 在行的落点里只记到点了没有，`EndTreeDrop`（列表建完之后）才 `SetOpen` / `ToggleRepo`、请求下一帧。
  指针不动时 mygo 不会产生新帧，要 `c.After(还差多久)`。Tester 的 `Release` 不带修饰键，⌥ 松手复制只能在 app 包里调 `DropTree(…, alt)` 测。
- 真窗口里拖文件树的行要先点一下选中（没选中的按住拖是拖着选）。合成拖动用 CGEvent 发按下 → 若干拖动 → 松开（一个小的 Swift 程序就够，
  坐标是窗口左上角起的 pt，每一下前确认前台进程是它）；两个动作之间停 1 秒，别被当成双击。

- 只有图标的按钮灰着时别调 mygo 的 `Disabled`：它不出悬停提示，按钮上又没有字，用户不知道为什么点不了。提交按钮（`ui/gitbar.go` 的 `commitRow`）
  是 `toolStyle(c, b, false, 灰)` 画灰、`能点 && b.Clicked()` 才做，提示里用 `withWhy` 写原因（`app.CommitWhy`）。要键盘能按到用 `wui.ButtonBase`，
  `squareButton` 的 `Box` Tab 到不了。

- **右键菜单的窗口大小**：在元素的事件处理（`HandleInput` 的回调）里取 `c.Root().Bounds()` 是 0×0——那个 `c` 是建树时的，事件来时根还没排。
  `PopMenu.OpenRow` 按 0×0 的窗口排，菜单只剩 180×32 的小块，右键菜单看着就是“闪一下”。窗口大小、元素自己上一帧的位置都要在**建元素时**取好
  （`b := el.Bounds()`；`root := c.Root().Bounds()`），闭包里用；事件里只能信这两个快照。
- **右键会带来一个 `dy=0` 的滚动事件**：macOS 的右键、触摸板的相位事件会送来 `InputScroll` 且 `DY` 是 0（日志里 `dy=-0.00`）。
  `PopMenu.Scroll` 原来只要指针不在菜单里就关，右键菜单会在刚打开那一帧被这个滚动关掉；小于 0.01 的滚动当成没滚。

## 键盘和快捷键

- 菜单快捷键先于编辑器接到按键（mygo 在 darwin 上没有覆盖 performKeyEquivalent）：编辑器有焦点时 ⌘K 照样打开 `>git ` 面板，
  mygo 编辑器的 ⌘K 和弦（⌘K ⌘0 等）一个都到不了编辑器。没有菜单栏的地方（测试、`OnShortcut`）才是编辑器先接，因此测试中能使用 ⌘K 和弦，不代表真实窗口中也能使用。
- 窗口快捷键会盖掉 mygo 编辑器自带的同一个键：⇧⌘G（找上一个）给了 Git 侧栏，⇧⌘H（替换）给了搜索页的替换（当前文件里的替换走 ⌥⌘F），
  ⇧⌘\ 给了 `view.openInOtherGroup`（编辑器的 `MoveToBracket` 还有 ⌃M）。`TestShortcutConflicts` 只认识注册表里的命令，不知道 mygo 编辑器的键位，
  所以加窗口级快捷键前先查 mygo `ui/editor` 的 keymap，被盖掉的要在 `docs/shortcuts.md` 里改成剩下的那个键。
- 同一个键在不同标签上是不同命令（⌥⌘F：Diff 标签是全文 diff，文件标签是替换）。按键和菜单项都走 `a.RunKey(mods, key)`，它按注册顺序查找第一个当前可执行的命令；
  别在菜单项或 `OnShortcut` 里直接 `Exec` 某个 id。
- 窗口快捷键会在终端前面接走键。同一个键既是窗口快捷键又要给终端（⌘W、⌘K），只能在注册表里加一条 `Context: Terminal` 的命令、排在全局那条前面，
  让 `RunKey` 优先处理终端命令。只在终端里有意义的键（⌘D、⇧⌘[）别注册成窗口快捷键，走终端的 `Options.OnKey`。
- 快捷键冲突按分派域算（`app/keybind.go` 的 `keyDomain` / `clash`）：窗口（全局、Diff 标签、编辑器）、终端、文件树、提交框、搜索结果各自为一个分派域，
  同一个键在两处各是一个命令不算冲突；窗口里上下文相同或有一个是全局才算。新加一个自己接键的地方（列表、输入框）要给它一个 `Context`，并进 `keyDomain`。
- 这个应用里 ⌘1–9 是 Git 栏跳到第几个仓库的段（`TestJumpRepo`），不是第几个标签。测试里换标签用 `a.SelectDoc(i)`。
- ⌘C / ⌘V / ⌘A 在真窗口里到不了 `OnShortcut`：菜单栏的编辑菜单会接收这几个键，作为 `InputCommand`（`copy` / `paste` / `selectAll`）发给有焦点元素的 `HandleInput`。
  - 文件树用 `HandleInput(treeCommands(a))` 接（不是这几个命令的返回 false）。编辑菜单项只在焦点元素能做时才亮，`HandleInput` 不接的再当按键发给快捷键，不会两边都跑。
  - `tt.Command("copy")` 测的是这条路；`tt.Key(Cmd, KeyC)` 测的 `OnShortcut` 只在测试里有用。
  - 菜单栏里有项的键（⌘D 是终端的"左右分屏"）由菜单接到、`RunKey` 分派；`RunKey` 也看 `Context: FileTree` 且有 `Run` 的命令（`a.TreeFocused` 时）。
- 编辑器中使用 ⌥ 快捷键可能额外输入一个字符。mygo 的 `keyDown:` 先发 KeyPressed，只要键盘在文字输入里、没按 ⌘ / ⌃，无论按键由谁处理，都会继续执行 interpretKeyEvents，
  ⌥ 打出的字（⇧⌥F 是"Ï"）随后以 InputText 送进编辑器；登记在窗口上的键（`OnShortcut`）还会让编辑器的 OnInput 连 KeyDown 都看不到。
  所以编辑器里只带 ⌥ 的键：命令带 `Local`（不登记在窗口上），由编辑器的 OnInput（`ui.docKeys`）接 KeyDown、吞掉紧跟着的那段 InputText。
  Tester 不会自己送这段字，测试里接着 `tt.Type("Ï")` 模拟。
- `wui.SearchField` 里面的输入框拿不到：`OnTextKey`、`TextSelection` / `SetTextSelection` 对它没用（接在外面那层上）。⇧⏎ 也照样发 `Submitted`，
  而 `c.Modifiers()` 是建帧时按着的键、不是那下回车的，真窗口里 ⇧ 先松开就当成 ⏎（测试里看不出来）。要分 ⏎ / ⇧⏎、要光标的框用 `TextInput` / `TextArea` + `OnTextKey`
  （`k.Mods` 是那下按键的），搜索栏的框因此是自己的 `searchBox`。框从单行换成 `TextArea` 的那一帧焦点给不上（新框还没建），下一帧再 `Focus` / `SetTextSelection`（`placeCaret`）。
- `OnTextKey` 一个元素只有一个，后设置的处理函数会覆盖先前的处理函数。单行搜索框的 ⏎ / ⇧⏎ / ⌥⏎ 都在 `shiftEnter` 一个处理函数里分；多行的 `TextArea` 也只有一个（⏎ 和 ⌥⏎ 搜，⇧⏎ 放给框换行）。
  别在别处再给同一个框挂一个。
- 窗口级的 Esc 用 `OverlayShortcut`：它只拿焦点链上没人用的键，编辑器收起选区、查找条、对话框的 Esc 都先轮到。注册成普通快捷键会覆盖编辑器自身的 Esc 处理。
- 点击工具栏按钮后，键盘焦点会移到按钮：按钮 `Focusable`，点完 `tt.Key` 或真键盘的 `=` `-` `0` 都落到按钮上，画布收不到。图片工具栏点完设 `imgFocus`，
  画布下一帧拿回焦点；UI 测试里点完要 `tt.Frame()` 一帧再按键。别的"点按钮后该接着按键"的地方一样。
- `menuSignature` 要带上 `keyRev`（`app/menu.go`）：菜单栏只在签名变了时重建。快捷键改了（`applyKeybindings` 每次加一）或正在录键（`KeyRecording`，录着的时候菜单栏不带键）
  不进签名的话，菜单仍会使用旧快捷键。
- 键从注册表读，别写死：右键菜单项用 `shortcut(it, id)`，元素自己接的键用 `onKey` / `app.KeyOf(id)`（`ui/keybind.go`），帮助文字里的键用 `app.Find(id).Keys()`。
  写死 `wui.Modifiers` + 键的，用户改键后，菜单标注会与实际响应的快捷键不一致。改了命令跑 `go test -run TestShortcutDoc ./internal/app/ -update`。
- 提示文字里的键也别写死（`app/commands.go`）：
  - 句尾括号里的用 `app.Tip(text, id)`（没键时不加括号；中文全角括号、英文半角）。
  - 句子中间的用 `app.KeyHint(id, 有键的句子, 没键的句子, args…)`，两句都写成完整的字面量（i18n 目录只收字面量），没键的那句改说命令名，别拼出一对空括号。
  - 列几个命令的键用 `app.KeyList`。`KeyText` 对不存在的 id 直接 panic，id 写错会导致测试失败。
  - 只写修饰键的（"⌥点""⇧回车"）、列表和输入框自己接的键、编辑器自己的撤销留字面。
- 换一个全局快捷键要连带改的：测试里按那个键的地方（比如 `ui/terms_test.go`）、`keyNames` 里没有的键（⌃⇧` 要加 `KeyBackquote`）、
  `docs/shortcuts.md`（`TestShortcutDoc -update`）、README / README.en 里写的键。修改前先搜索所有引用。

## 编辑器

- `editor.State.Label` 会把编辑器登记成文本框标签，Tester 里可以 `tt.Click("编辑 a.txt")` 聚焦。
- 有焦点的编辑器会持续绘制：mygo 的 `ui/editor` 只要建的那一刻 `Focused()` 为真，就每 500 ms 排一帧闪光标，没有开关；帧内存 2 秒没有新帧才放，
  因此会额外占用一两百 MB 内存，并持续消耗 CPU。新加的编辑器 / Diff 视图要走 `ui/caret.go` 的 `restingEditor` / `restingDiff`（停手 30 s 不闪），别直接用 `editor.View` / `editor.DiffView`。
- mygo 编辑器在光标移动后自动滚动，只给光标上下留 min(3 行, 半屏)，没有选项可以改（像 VS Code 的 cursorSurroundingLines 那样的选项它没有）。
  `restingEditor` 先让 mygo 滚完，然后由 `caretMargin` 补到 min(5 行, 视图高 1/5)。只有键盘移动了光标才补，靠 `Rest.Keyed`：`OnInput` 里收到按键 / 文字 / 命令时为真，指针按下时为假，每帧用完就清（不然没挪光标的 Esc、⌘S 留下来，之后内容从外面变了会把滚走的视图拽回光标）。
  这样点击、滚轮、拖滚动条都不受影响，程序里调的 `GoTo` 本来就滚到正中，也够余量。光标所在行被软换行折成几行时不补，因为公开接口拿不到光标落在第几折。
- `DiffState.GoToHunk(i)` 把光标放到那块开头，不在视图里才滚、滚到正中（等视图排过版才滚）。mygo 没有公开的滚动 API，所以"第一处改动"在视图正中，不是上三分之一。
  `ExpandAll` / `CollapseAll`（局部 ↔ 全文）不保留改动的位置，之后再 `GoToHunk` 一次（`Doc.ToHunk`）。
- `editor.State.LineBackgrounds` / `GutterMarks` 记的是设的时候的行号，不会随编辑自动移动：插一行之后底色还留在原来那几行。冲突块的染色（`ui/merge.go` 的 `conflictMarks`）
  所以每帧照现在的块重设，没有块就置 nil；块本身按 `Buf.Version()` 缓存（`Doc.ConflictBlocks`），每帧重设不会增加大量计算。别的地方要按内容染色也照这个来，别只在打开时设一次。
- `editor.Buffer.Modified()` 只要当前文本和存盘文本不是同一棵 rope（改过，哪怕又撤回到一样），就把两份都拼成整串再比较，大文件一次几 MB 分配。
  每帧要问的地方（标签圆点、状态栏、菜单）一律走 `Doc.Modified`，它按缓冲区指针、`Version()`、存盘次数缓存。`Buffer.MarkSaved()` 不动 `Version()`，
  所以 Holt 里只经 `Doc.markSaved` 调它（顺带让缓存失效），别直接 `d.Buf.MarkSaved()`。
- `GutterMarks` 归冲突块，诊断只走 `SetDiagnostics`（`app/lint.go`）：`conflictMarks` 每帧整个重设 `GutterMarks`，别的东西往里放下一帧就没了。
  检查报的问题交给 mygo 编辑器自带的诊断（`st.SetDiagnostics`：波浪线、悬停、滚动条标记），装订线不画点。
- `SetDiagnostics(version, ds)` 的 version 是 ds 按哪一版文字算的，编辑器沿那一版之后的编辑历史把位置挪到现在。缓冲区未保留那段历史（重读过、版本对不上）时什么都不设，也不报错。
  所以（`SyncProblems`）：先 `SetDiagnostics(cur, nil)` 清掉，再按记下的版本设；设完 `Diagnostics()` 还是空的，就按现在的文字重算、用现在的版本设。
  位置按跑命令那一刻的文字和版本算（`snapOf` 在保存 / 手动检查时拍下），不要在结果返回时按当前文本计算。
- 语言服务器的位置是 UTF-16 码元（LSP 默认），不是字节；换偏移用 `internal/lsp.Text`（协商成 `utf-8` 时按字节），别拿 `Position.Character` 直接当字节列。
  诊断按服务端给的那一版文字算偏移，再交给 `SetDiagnostics(那一版, …)` 让编辑器挪；把 LSP 和外部检查两批合在一起时，各自先按自己的版本设、`Diagnostics()` 读回来，最后用当前版本重设（`app/lang.go`、`app/SyncProblems`）。
- 补全 / 悬停 / 跳转定义的钩子是 `editor.State` 的字段（`OnCompletion`、`OnHover`、`OnDefinition`），每帧在 `ui/tabs.go` 里设；`OnHover` 的回答用 `St.ShowHover(start, end, 纯文字, nil)` 显示，Markdown 记号要自己去掉。
  补全列表由编辑器按前缀再过滤一次，服务端给的 `textEdit` 起点和光标前的词不一样时，Holt 只按词替换（`mapCompletions`），做不到服务端那种任意范围替换。
- 编辑器的 `OnInput` 是 View 那一刻取的，后设置的处理函数会覆盖先前的处理函数。`restingEditor` 自己挂了 `OnInput`，额外的键要串进它的 `keys` 参数（比如 `mergeKeys`），不能在外面再 `.OnInput(...)`。
  返回 true 会消耗该按键事件（⌃⌥1 不会再插个字）；只接 `InputKeyDown`，键按命令注册表比（`ev.Mods == cmd.Mods`，`Key` 或 `KeyUnknown` 时的 `PhysicalKey`）。
  这类本地命令（`Run` 为 nil）不进窗口快捷键、不在面板里。Tester 里 `tt.Key(wui.Ctrl|wui.Alt, wui.Key2)` 能走到它；真窗口里 ⌃⌥ 的键会不会被输入法吃掉要手测。
- `editor.NewBuffer` 可以在后台建：它只建 rope、不碰界面，`Buffer` 的方法从哪个 goroutine 调都安全。大文件的缓冲区在 `ReadFile` 里建好，主线程只换指针。
  换 `St.Buffer` 的指针时 mygo 会重设选区、折叠、查找和滚动，要留着的（光标、`ScrollY`）先记下、换上后放回去（`docs.setText`）。
- `Buffer.Reset` 把整段旧文字记进撤销日志（mygo 留 256 条），重读几 MB 的文件每次都多留一份旧的。大文件重读不走 Reset，换一个后台建好的新缓冲区。
- 重读时 `Loading` 不置上：标签已经有缓冲区时重读不清空它。测试里等重读完成要看内容（`strings.HasSuffix(d.Buf.String(), …)`）或 `Gen`，别等 `!d.Loading`。
- `unsafe.String` 仅用于调用方独占的字节数据：`decode(data, …, owned=true, …)` 把 UTF-8 的 `data` 直接借成字符串、不拷，调用方之后不能再改 `data`
  （`ReadFile` 读的、`git.Blob` 返回的可以）。别人传进来的字节用 `Decode`（会拷一份），由 `TestSameBaseHash` 检查这一约定。
- mygo 的 `Highlighter` 返回"还没好"会让编辑器每帧重画：`Highlight` 返回 false、或 `Tokens` 返回 final 为假，编辑器都会接着要帧（后者每帧重问看得见的、没准的行）。
  后台高亮的 `Highlight` 因此总是返回 true，`invalidate` 只在越过要的行和算完时各调一次；别在每批里都调，也不要让未完成的行长期保持 final 为假。
- mygo 换掉高亮器时不调 Close（接口里没有）：换语言、换缓冲区、撤销日志对不上重建，都会直接丢掉旧的。自己开 goroutine 的高亮器要算完自己退出、`Edit` 时停下旧的，
  不能常驻等待新任务。测试用 `highlight.Running()` 等它们都退出。
- `editor.State.Run` 不管 ReadOnly（mygo 只在键盘输入的路上查）：只读的标签（历史版本、大文件）上 `St.Run(editor.DeleteLine)` 照样改文字。
  命令面板里的编辑器命令必须在 `Run` 里自己查 `Enabled`（`inEditorChecked`），不能仅依赖面板中的禁用状态，`Exec`、自定义快捷键、菜单都会走 `Run`。
- 处理全文的 mygo 编辑器命令在主线程上计算：全部折叠 / 折叠到第 N 级 / 递归折叠要算所有折叠范围，选中所有相同的、拆成每行一个光标要扫整篇。
  64 MB 的文件上全部折叠五分钟没完、拆光标 18.7 s，所以命令表里标了 `whole`，超过 `MaxWholeTextBytes`（1 MB）时禁用。往表里加命令时先在 64 MB 的文件上试一次。
- 叠在编辑器上的层会盖住编辑器自己的查找条（`ui/sticky.go`）：查找条、跳到行条是 mygo 编辑器在自己里面建的，后建的 `Absolute` 叠层画在它们上面、也先接点击。
  要知道它们在哪，就换 `State.RenderFindBar` / `RenderGoToLine`（记下 `bar.Bounds()` 再调 `editor.DefaultFindBar` / `DefaultGoToLine`），叠层右边停在它们左边；拿到的是上一帧的位置。
  mygo 也没有"装订线多宽"的接口，`stickyGutter` 是照 editor 的 `gutterWidth` 抄的估算，mygo 改了装订线要跟着改。
- blame 装订线画的时候还要核对滚动：编辑器在 `Draw` 里自己还会滚（光标跳出视图、拖选到边上、软换行量出新的行高），建界面时读的 `ScrollY` / `LineTop`
  到画的时候可能已经变了。列上的 `Draw` 比一下，变了就 `Invalidate` 再建一帧，否则装订线会与代码错开一帧。
  装订线的宽用编辑器的左 `Margin` 留出来（不是在编辑器外面套一列），滚轮才落在编辑器上。
- 长期保存的大纲名称需要 `strings.Clone`（`app/symindex.go`）：按行正则认出来的 `Name` / `Detail` 是整篇文字的子串，存进索引不拷贝就把整个文件的文字留在内存里。
  索引看文件列表变没变靠 `files.Tree.Paths` 的切片身份（`samePaths`）：`Paths` 只整体换，别在原地 `append` / 改它。
- 共用缓冲区的几份标签，读盘之后要 `relink`（`app/mirror.go`）：镜像和正本共用 `*editor.Buffer`，但 `Doc.ApplyFile` 读到新文件时换的是缓冲区指针。
  读盘、保存、冲突横幅、保留我的、换编码 / 语言 / 缩进 / 换行之后都要 `a.relink(d)` 让别的几份 `Share` 过来，否则修改一份标签后，其他标签不会同步。
  新加一条会换 `Buf` / 对比基准 / 编码的路径也要接上。没保存的计数、退出名单、监听重读只认正本（`Copy == 0`），别数重了。
- 文件标签写盘只走 `WriteDoc` → `diskWrite`（`app/localhist.go`）：保存、全部保存、退出时保存、自动保存、格式化后保存、转成 UTF-8 都经它，
  本地历史（写之前记"保存前磁盘上的"、写完记"保存"）和 keybindings.json 的重读靠它。新增文件标签保存入口时，不要直接调用 `os.WriteFile`，否则会绕过本地历史；
  应用里别的写盘（重命名 / 移动、恢复、`>restore`）自己调 `LocalHist()` 记。
- 设置文档的保存不写盘：`docs.SettingsDoc` 是可编辑的内存文档，`Save` / `WriteDoc` 见到它交给 `applySettingsDoc`。加新的保存入口时不要让它进入文件写入流程（它没有 `Abs`）。
  设置从别处改了（设置窗口、⌘=、导入），`SettingsChanged` 里 `syncSettingsDocs` 换掉没改过的设置文档，改过的不动。

## 终端和 termd

- `cmd/workbench` 的 `main` 第一行派发 termd：`Holt termd <sock> <log>` 是终端后台进程，`if len(os.Args) > 1 && os.Args[1] == "termd"`
  要在解析参数、单实例锁、载字体、开窗口之前。别在它前面加东西：后台进程不能开窗口，也不能抢单实例锁。
- 终端的 `Options.OpenLink` 在主线程、终端锁外面调，里面可以调 `Terminal.Dir()`；返回 false 时网址交给系统浏览器，路径什么都不做。
- 终端的光标闪烁（`terminal.Options.NoBlink`）只在 `terminal.New` 时定，运行中改不了（只有字体、主题、选中即复制、最低对比度能改），所以设置里的"光标闪烁"仅对新终端生效。
- mygo 的终端不给 shell 的进程号，`ps -E` 在 macOS 上也看不到别的进程的环境。要进程号只能找应用的子进程、按启动顺序认领；
  关掉还没认领到的格时等 `Term.Done()`（最多 300 ms），否则它的 shell 可能被下一个分屏认领。
- 刚起的 shell 还没认领到进程号时，`TermPane.Job()` 是 `"?"`（当作"可能有命令在跑"）。测试里先等 `Job() == ""` 再换工作区 / 关终端。
  空工作区里底栏开着会起一个 `~` 的 shell，换工作区时就会问"关掉终端？"：测空工作区的 UI 测试先 `termState(t, false)` 再 `newUI`（第一帧就决定了要不要起 shell）。
- 终端的 `Text()` 去掉了每行末尾的空格：等提示符时别等 `"$ "`，等 `"\n名字 $"` 这类不带尾空格的。
- 接在 termd 上的格（`ResizeByConn`）只在收到 `resized` 时才 `Term.Resize`：改大小只是发给 termd（`Conn.ResizePixels`），屏幕按 termd 回的大小排。
  本地若先调用 `Resize`，下一镜像帧仍按 termd 的旧尺寸排版，会造成错行。`attachPane` 里 `conn.OnResized = term.Resize`，别的地方别调。
- 镜像帧不带 OSC 7：`terminal.Mirror` 的帧只有屏幕和回滚，shell 报的目录 termd 单独发（`OnDir`），窗口里的 `Term.Dir()` 在后台格里是空的。
  要目录用 `shellDir()`（先 `Term.Dir()`、再 termd 发来的 `rdir`），没报过的 termd 按进程号读 cwd。
- 后台格里别在本地 `ClearScreen`：⌘K 交给 termd 清（`conn.Clear`，`OnClear` 也接到它），本地清了下一个镜像帧又把内容画回来。全屏程序里 termd 回 `N`（不清屏）。
- 设了 `WORKBENCH_STATE` 的实例有自己的 termd：套接字跟着状态目录（`termd.SockPath(StateDir)`），不会接到日常开着的那个 Holt 的会话。
  用完只 `kill -TERM` 自己这个 termd（`pgrep -f '<自己的 .app>/Contents/MacOS/Holt termd'`），别杀别的。
  套接字 0600、协议不认证，能连接套接字的进程就能向 shell 发送输入，别把它放到别人能读的目录里。
- 恢复上次输出必须早于 shell 的首次输出（`app/termhist.go`）：`Terminal.History()` 的序列最后让光标停在它下面一行，`startLocal` 建好终端马上 `Feed`。
  晚了（shell 读完 rc、提示符已经出来）就会盖在提示符上或者夹在两行之间。连接 termd 的分屏不恢复这份输出，因为镜像帧自带回滚；连接失败并退回本地时仍需恢复。
- termd 里退出了的会话由两端分别管理：termd 这边，shell 退出了、没有窗口接着的会话过 `Server.Linger` 拿掉（接回去过就作废，`session.linger` 代数对不上）；
  窗口这边，关窗口 / 换工作区时退出了的格直接 `Kill`（被别的窗口接走的 `lost` 只断开），`sessionTerms` 只记没退出的会话号。
  改其中一边要看另一边：仅依赖 termd，下次打开窗口可能先连到即将清除的会话；仅依赖窗口，窗口崩溃后留下的会话无法清理。
- termd 的平台相关部分按文件拆：`pty_unix.go` 是两边一样的 `proc` / `startProc`（Setsid + Setctty，伪终端当控制终端），
  `pty_darwin.go` 用 grantpt / unlockpt / ptsname 三个 ioctl，`pty_linux.go` 用 `TIOCSPTLCK`（传 0 解锁）、`TIOCGPTN`（读从端编号）再开 `/dev/pts/N`；
  `<sock>.lock` 在 unix 用 `flock`、Windows 用 `LockFileEx`。Windows 上还没有 ConPTY：`startProc` 直接返回 `ErrNoPTY`、`Spawn` 不起 daemon，
  应用退回窗口里的本地终端。加平台时 `session.go` 用的 `proc` 字段要照样有。

## 窗口、标签和生命周期

- `mygo.Dialog.Open` 会阻塞，放 goroutine 里，结果经 `update` 回主线程。它一次要么只能选文件、要么只能选目录，所以"打开文件夹…"和"打开工作区文件…"是两项。
- `Ask` 一次只能显示一个确认框：同时需要确认多件事时，应合并为一次询问（`OpenArgs` 里几个不在列表里的仓库），别连着调 `Ask`，后一个会覆盖前一个。
- 后台 goroutine 要用的文档字段（`Abs`、`Rel`、`Root`、`Change`……）在 `go func` 之前拷到局部变量：重命名（`Doc.Moved`）在主线程改它们，
  在 goroutine 中直接读取 `d.Abs` 会触发 `-race` 的数据竞争报告。
- 注册表 `Commands` 里的 `Run` 引用了会用到 `Commands` 的函数时会出初始化循环，这类在 `init()` 里填。
- 换工作区会整个重写 `*a`（`switchTo` 里 `*a = *n`）：
  - goroutine、`time.AfterFunc`、终端回调（`OnTitle` / `OnExit`……）里读 `a.Update` / `a.Win` 会与重写操作竞争，导致 `-race` 报错。
    在主线程上先取出来再带进去：`up := a.Update` / `up, win := a.Update, a.Win`，然后 `go func() { …; up(func() { … }) }()`。
    `cmd/workbench` 的计时器用 `win.Update`。菜单的 Click、按键回调本来就在主线程上，不用。
  - `App` 上新加的跟着窗口走的字段要在 `switchTo` 里带过去，没带的会被清掉。现在带的是 `Windows`、`Slot`、`LastBar`、`ownStore`、`Win`、`Update` 和各个回调；
    漏了 `Windows`，换完工作区注册表就无法再正确管理该窗口（菜单、存盘都错）。
- 存盘一律经 `a.store()`（注册表的 `Store`），别直接 `workspace.SaveState`，否则会覆盖其他窗口的会话。
- 界面状态不要放在包级变量中：几个窗口都在主线程上画，包级变量会被轮流改（比如一个全屏、一个不全屏时每帧都记一笔日志）。放到 `App` 上（`a.LastBar`）；
  只有跟着进程走的（设置、语言、主题）才是全局的。
- 绘制标签条的循环中可能发生 `Drop`、右键菜单的"关闭"之类改切片的事：遍历 `slices.Clone(a.Docs)` / `slices.Clone(a.TermTabs(r))`，
  别直接 `range` 原切片（边遍历边改会错位、Key 重复）。
- 编辑器组分屏时 `a.Docs` 只是当前有焦点的组的标签，别的组的在 `Group.docs` 里，换组时 `swapTo` 对调。改"当前标签"的代码照旧用 `a.Docs` / `a.DocSel`；
  要所有标签（会话、关窗口前问没保存的、找已经开着的文件、Git 状态变了改 Diff 标签）一律用 `a.AllDocs()`，按 `GroupOf` / `locate` 找在哪组。
  测试里看各组用 `a.Groups()` + `a.GroupDocs(g)`。
- 分屏时键盘在哪组：`groupCard` 的 `FocusWithin` 由假变真那一帧调 `FocusGroup`，不要每帧根据 `FocusWithin` 设置当前组（程序刚换了组、键盘还没过去的那一帧会被换回来）。
  程序换组走 `toGroup`（置 `a.FocusDoc`，下一帧那组卡片先 `Focus()`、编辑器再拿）；换组前把原来的编辑器 `Rest.Woke = true`，
  否则停止闪烁的编辑器会每帧取回焦点，导致无法聚焦其他组。
- `FocusDoc` 是全局的"下一帧把键盘给键盘所在组的当前标签"，那个标签还在读（`Loading`）就一直等着。测试里先 ⌥点开一个文件（置 `FocusDoc`）、
  没等它读好就程序换组 + `a.FocusTree = true`，读好的标签会把键盘抢回去，文件树只拿到一帧。测试里要先换组再给文件树键盘：
  先等那个文件 `Buf != nil`，或照用户的做法点那组的编辑器（`mustClick(t, tt, "编辑 x")`）。
- 松开拖着的标签是在画某一组的时候处理的，那时 `collapse` / `splitOff` 已经就地改了树（`cut` 是 `*n = *兄弟`）。`edNode` 画两半之前先把 `n.A`、`n.B`
  拿到局部变量，否则绘制后半部分时拿到的可能是 nil。新的树下一帧才画。
- 会话的 `Tabs` 按树的顺序存（左上到右下、组里从左到右），不是打开的顺序；`Layout` 的叶子记的是这个顺序里的下标。
- 会话里不是文件的标签，`path` 也要写绝对路径：`State.clamp` 会删掉 `path` 不是绝对路径的标签，后面的 `active`、`layout` 下标就全错位了。
  Diff / 历史版本标签的 `path` 写仓库根，`restoreTabs` 看到是目录就跳过、留空位，下标对得上。以后再加别的种类也照这样。
- 只读的生成标签（搜索编辑器、替换预览）不进会话、不进 ⇧⌘T：`sessionTab` 是白名单，新 Kind 默认就不记；`RemoveDoc` 的 `closed` 是黑名单，新 Kind 要自己加。
  后台生成的内容回主线程前先比 `d.Gen`（标签可能已经重写过或关了），关标签时 `searchSrcOf(d).stop()` 取消它在跑的搜索。
- mygo 的菜单栏是整个应用一份（`mygo.App.SetMenu`）：几个窗口时只有前台那个 `App` 在 `SyncMenu` 里重建它（`Windows.Focus` 清 `menuSig`），别的窗口每帧都不碰。
  菜单项的 `Click(item, win)` 交给 `a.target(win)`（点的时候在前面的工作台窗口），别在 Click 里直接用建菜单的那个 `a`。
  Tester 里没有菜单栏（`HasMenu` 为假），这条只能靠 `TestWindowsRouting` 直接调 Click 测。
- 退出确认应在 `OnBeforeQuit` 中处理：mygo 先发 `OnBeforeQuit`，再一个个关窗口，哪个窗口的 `OnClose` 拦了就停，但前面已经关掉的不会回来。
  所以 ⌘Q 时要问的都在 `Windows.RequestQuit` 里：拦下、一个个问，问完自己 `Quit()`（`ending` 置真，这次放行）；问着时又来一次 ⌘Q（`quitting` 不为 nil）
  只将正在确认的窗口置于前台。不注册 `OnWindowAllClosed`，`Windows.Closed` 关到最后一个自己退。
- 换工作区是 `*a = *n`（`app/workspaces.go`）：指向 `n` 里字段的指针拷过来后还指着 `n`。`Repo.set`（`&a.St.Settings`，Git 栏显示工作树 / 贮藏的开关读它）
  所以在拷完之后对每个仓库重新指一遍；以后再往 `Repo` 里放指向 `App` 字段的指针也要这样。
- `Window.OnReadyToShow` 只来一次（窗口画完第一帧时），之后再注册的不会再来。还原队列靠它推进：main 的 `NewWindow` 钩子里紧跟着注册
  `win.OnReadyToShow(func() { wins.Ready(a) })`。没画完第一帧就关掉的窗口等不到它，所以 `Windows.Closed` 里替它调一次 `Ready`。测试里没有真窗口，直接调 `w.Ready(a)`。
- `RestoreGap` 的计时器要回主线程：`time.AfterFunc` 在别的 goroutine 上跑，到点只做 `mygo.RunOnMain(fn)`，`fn`（`Windows.next`）在主线程上读写注册表。
  `Windows.Later` 是这个钩子，测试里换成记下来手动跑的桩。
- `mygo.App.SetWindowStateStore` 必须在 Run 之前：mygo 在建第一个带 `StateKey` 的窗口时读一次窗口位置（只读这一次），之后再换只改了往哪写。
  所以 main 在 `WhenReady` 之前就定 `stateDir`（`WORKBENCH_STATE` 或 PathUserData）并设好 `app.WindowStates`；`App.Path` 在 Run 之前就能用。
- `mygo.App.Path` 第一次调就会建出空的目录，所以 `migrateDirs`（`cmd/workbench/migrate.go`，把旧名字 Workbench 的状态和日志目录改名成 Holt）必须在它之前。
  可执行文件名不是 Holt（`go run`）或设了 `WORKBENCH_STATE` / `WORKBENCH_LOGS` 时不搬。迁移只在测试的临时 HOME 里试（`TestMigrateDirs` 设 `HOME`），
  绝不对真的 `~/Library/Application Support/` 试。为什么 `cmd/workbench` 不改名见 `AGENTS.md`。

## Git

- `git.Run` 出错时也可能带着输出返回，判断错误应检查 err，不要根据输出是否为空判断。
- 路径比较要解开符号链接：`git worktree list`、`git rev-parse --show-toplevel`、`AddRepoDir` 给的是真实路径（`t.TempDir()` 的 `/var/…` 在它们那里是 `/private/var/…`），
  工作区里记的、拼出来的可能不是。判断"已打开"、套在仓库里面、分支在哪检出、子模块是不是已经加进来，一律用 `git.SamePath` / `git.Under`，别用 `==` 或 `strings.HasPrefix`。
- `git log --follow` 加 `--grep` / `--author` 会丢掉改名之前的提交：文件历史里搜索时读整段（`git.MaxFollowSearch`）在 Go 里筛。
- 提交图要 `git log --date-order`：默认的时间顺序在提交时间乱了（变基、改过时钟）时会把父提交排到子提交前面，`internal/graph` 只往前算，连线就断了。
  `--date-order` 保证父在所有子后面，其他提交仍按时间排序；在 5 个仓库的工作区上读 200 条多 0.02–0.05 s。
  有搜索词、看文件历史时读到的只是一部分提交，不算图（`History.Graph` 空着）。
- `graph.Lanes.Next` 汇入时会回头改上一次返回的 `Row.Edges`（同一块底层数组）：`History.Graph` 直接 append 返回的 Row，不要在传入下一提交前复制或排序 Edges，
  否则会丢失上一行的弯曲连线。分页时继续向同一个对象传入提交： `Lanes`（`TestPaging` 检查分页结果与一次性处理是否一致）。
- `graph.Lanes.Reserve` 只在第一次 `Next` 之前有用（`active` 不空时什么都不做）；分页时不要每页再 Reserve。`History.lanes` 在 `reset` 时清成零值后马上 Reserve，
  HEAD 的哈希要在后台自己 `rev-parse`，别用 `Extra.Head`（状态和历史并发重读，可能是旧的）。
- 点已经展开的提交不会收起（`OnHistSelected` 只展开）：测试里要收起得点别的行。
- 监听要放过 `.git/worktrees/<名>`：每个链接工作树的 `HEAD`、`index`、`locked` 都位于该目录下。不看的话外部 `worktree add / remove`、在工作树里切分支，
  主仓库无法收到变更提示（`TestWatchWorktreeAdded`）。再深一层（`logs/` 等）照样不看。
- `git worktree prune --dry-run -v` 把会清的写在 stderr 上：`git.Run` 只拿 stdout，是空的，`PruneList` 用 `RunOp`（合并输出）。
  `worktree list` 一律加 `--porcelain -z`（路径里可以有空格、换行），解析时不认识的键跳过（以后的 git 会加）。
- 读取工作树时不要无条件执行命令：普通仓库先看 `<公共目录>/worktrees` 在不在（`Extra.CommonDir`），不在就没有工作树；改动数和领先 / 落后只在"工作树"块展开时算
  （一个有 45 个链接工作树的仓库是 90 条 git 命令，约 1 s）。
- 冲突解决成和 HEAD 一样时，`git add` 之后没有已暂存的：测试别假设 Staged == 1（用"他们的"那边才有）。合并停在一半、冲突都标完时，
  `Repo.MergeReady()` 让提交框照样能提交（git 允许这类合并提交），拣选 / 变基不行。
- 会停下来的 git 写命令（合并、变基、拣选、还原、`rebase --continue` / `--skip`）返回非零退出码不一定表示失败：做完这一步又停在下一个冲突上时退出码是 1。
  - 横幅的继续 / 跳过走 `app.runStep`（自己的 `Op.done`），输出里有 `CONFLICT (` 就当"又停在下一处冲突"。别用 `RunOpThen` 的 then，非零时它不跑。
  - 发起这类操作走 `stopOp`（`app/gitdaily.go`）：输出里有 `CONFLICT (`、或仓库还在进行中的操作里（空提交的拣选），就交给冲突横幅、只提示一句，别用默认的失败弹窗。
  - "HEAD 里已经有了"（`inHead`）只照右栏读进来的历史走父提交，读不到的算不在。
  - git 2.54 的拣选 / 还原序列不留 `sequencer/done`，只有 `sequencer/todo`（第一行是停着的这个），所以横幅只写"后面还排着 N 个"。
  - `git merge --skip` 不存在；`cherry-pick --continue` 解决成空的会失败（"is now empty"），`rebase --continue` 空的会直接丢掉这个提交。
- `git rebase -i` 停在 edit 时返回 0：交互式变基跑完 `o.done` 拿到 `err == nil` 不代表变基已结束，需先检查 `git.ReadOngoing(gitDir) == git.Rebasing`（冲突时非零，也照样先看它）。
  - todo 里 `exec` 行的命令过 `sh`，路径用 `shellQuote` 包（单引号，里面的单引号写成"结束引号、`\'`、再开引号"）。
  - `GIT_SEQUENCE_EDITOR` 只经 `Op.env` / `git.RunOpEnv` 给这一条命令（加在默认环境后面，同名的后加入的值覆盖默认值），别设进进程的环境。
  - todo 和信息文件停着时不能删（后面的 exec 还要读）。变基结束（`followRebase` 看到 `Ongoing` 离开 `Rebasing`，再 `ReadOngoing` 核对一次）才删。
- `--numstat -z` 的改名 / 复制不在同一段里：普通的一条是 `加\t删\t路径\0`，改名时路径那格是空的，后面再跟 `旧路径\0新路径\0` 两段；二进制文件的加删是 `-` / `-`。
  `git.addNumstat` 按新路径对回 `diff-tree` 列出来的文件，对不上的跳过（numstat 失败只是没数字，文件照样列）。别按 `\t` 拆整个输出，也别假设一条一段。
- `stash -u` 的未跟踪文件在贮藏的第三个父提交里：贮藏提交本身（工作区）和 `^1`（贮藏时的 HEAD）比是改了的文件，未跟踪的不在里面。
  有 `^3` 时再拿它和空树比（`diff-tree --root`），这些文件的 `Change.Commit` 是 `^3` 的哈希（开 Diff 时旧的一边是空的）。
  父提交用 `log -1 --format=%P` 读，别拼 `stash@{n}^3`（没有 `-u` 的贮藏没有第三个父提交，会报错）。
- `git restore` 对子模块什么也不做：子模块那一项（gitlink）的丢弃是 `git submodule update -- <路径>`（检出父仓库记录的提交），
  只有检出的提交变了（`Change.Sub` 带 `C`）才有得丢；里面有改动 / 未跟踪的文件是子模块自己的事，提示用户到子模块里处理。`DiscardAll` 跳过子模块。
  子模块记的提交不在父仓库里，`cat-file` / `show <commit>:<path>` 读不出来：看是不是子模块用 `ls-tree -z <commit> -- <path>` 的模式 `160000`。
- 行级暂存是正着打补丁：取消暂存 / 丢弃不用 `git apply -R`，而是在新的一边上写"改回去"的补丁正着打（`LinesEdits` 的配对规则见 [architecture.md](architecture.md)）。
  Diff 视图在以换行结尾的文件后面多一行空的，选到它不算一行改动。
- `git ls-files -s` 的已跟踪文件和未跟踪文件的输出格式不同：带 `--others` 时已跟踪的是 `mode sha stage\t路径`，未跟踪的只有路径（git 不记它是什么）。
  mode `120000` 是符号链接，指向目录的链接也列成文件。整棵读时别为了认链接逐个 `Lstat`（5 万个文件），仅对数量较少的 `Links` + `Others` 调用 `Readlink`（`files.ReadLinks`）。
- `git ls-files --others --ignored --exclude-standard --directory` 对里面只有被忽略文件的未跟踪目录列 `sub/`（目录本身没被忽略）：
  `files.Tree.Done` 把带 `/` 的都当整个被忽略的目录，展开时 `ReadLazy` 一层层读磁盘。
- `git check-ignore` 退出码 1 是"一个都没被忽略"，不是出错（`git.CheckIgnore`）；`--no-index` 让已跟踪的路径也照规则判。它认父目录和 `dir/` 规则，
  所以问空目录路径就够了，不用先拼 `/`。问不了（不是仓库、被取消）时空目录一个都不要，宁可少显示。
- 未跟踪目录那行不进 `OpenDiffs`（`app/gitchosen.go`）：`Change.Dir` 的行只是折叠的目录，开出来是一句说明；多选里有它时跳过，里面的文件要展开了选。
- `Status.DirFiles` 是后台补上的（`app/untracked.go`）：读完状态后同一个 goroutine 里 `git.ListUntrackedDirs`，最多等 50 ms，等不到先贴状态、列完再补。
  没列到的目录 `DirFiles` 里没有这个键（画"读取中…"），别把"没有键"当成"目录是空的"。同一个目录先用上一次列的（`keepDirs`）。
- "一个名字 + 一两个框"的 Git 对话框加进 `app.RefDialog`（`app/gitrefs.go` + `ui/gitrefs.go`）：新加一种就加一个 `RefDlgKind`，在 `refDialog` 里给标题 / 说明 / 按钮 /
  命令预览 / `run`，重名及 git 不允许的字符由以下函数检查： `refNameErr`，别另起一个 `Open` 字段。远程分支名拆远程和分支用 `splitRemote`（按已有的远程认，别 `strings.Cut` 第一个 `/`），
  推 / 删标签用 `TagRemote`。
- askpass（写命令要凭据时弹框）：
  - 写命令的 `GIT_ASKPASS` 脚本 exec 的是 `os.Executable()`，测试里就是测试程序。会跑写命令（`RunOp` / 克隆）的测试包要在 `TestMain` 里分出 askpass 模式，
    照 `app/askpass_test.go` 加三行；不分的话 git 一要凭据就把整套测试再跑一遍，还会卡住。
  - 脚本和套接字放 `os.TempDir()`，路径要引号：git 用 `sh -c` 起 askpass，路径带空格就断；macOS 的 unix 套接字路径不能超过 104 字节。
    `~/Library/Application Support` 两样都占，别挪进状态目录；脚本里 exec 的路径用 `shellQuote`。
  - 凭据变量只加在写命令的 `cmd.Env` 上，别 `os.Setenv`：终端（termd、进程内的 shell）继承应用的环境，带上了终端里的 git 也会来弹框。
- 面向用户编写的错误说明应包装为 `humanErr`：`Fail` 会拿 `git.Explain` 认错误原文，"取消后留下了锁文件"这种自己写的话里有 `index.lock`，不包就被"解释"成另一句。

## 文件树和文件系统

- 临时目录在 `/var` 下，`lsof` 读到的目录是解开链接的（`/private/var/folders/…`）：和仓库根比较两边都 `EvalSymlinks`。
- 复制进来的目录（从访达拖进来）自己递归复制（`copyIn` / `copyDir` / `copyNode`：`Lstat`，链接用 `Readlink` + `Symlink` 原样写，相对的还是相对的），别换回 `os.CopyFS`：
  它遇到符号链接会报错，而且文件只给 0o666 加上源文件的可执行位，别的权限位不留。单个文件的复制（`copyIn`）原样保留权限位。
- 文件树的增量刷新只在主线程改树（`app/treediff.go`）：
  - 后台 `treeDiff.run` 只读 `startTreeDiff` 那时的树根，改动回主线程 `applyTreeDiff` 再 `Tree.Apply`；那时 `Gen` 变了（整棵读过或正在读）就丢掉，以整棵的为准。
  - 后台不碰主线程的东西：快照里的 `Links` 是 `maps.Clone` 的一份（`Apply` 在主线程上改 `t.Links`），`Diff` 里别调 `filePaths()`（它在主线程上写每个目录的路径缓存）。
  - `Tree.Paths` 只能整个换一份新切片，⌘P / 搜索手里拿着旧的。
  - 删掉目录里最后一个文件时 `Tree.Apply` 会把目录一起收掉。要留下空了的目录，先 `AddEmpty` 记进 `Extra` 再 `Apply`。
  - 测试数 `git ls-files` 的次数换 `lsFiles` 变量。测试里把文件换成链接先 `os.Remove`：直接 `os.WriteFile` 到链接上写的是它指向的那个文件。
- 被忽略的目录行排在普通目录后面、文件前面，编写测试的期望顺序时需遵循这一规则。
- 找空目录（`files.FindEmpty`）：`find -empty` 会钻进子模块和嵌套仓库，只 `-name .git -prune` 不够，嵌套仓库里的空目录要按仓库路径筛掉（`dropNested`），
  否则会出现在外层仓库的文件树中。只列叶子空目录（有空子目录的父目录不空）；只有 `.DS_Store` 的目录不算空。超过 `files.MaxEmptyScan`（2 万个文件）的仓库不找。
- 文件树头部的按钮只在指针在侧栏里、或侧栏有键盘焦点时建。Tester 的指针默认不在侧栏，测试要点"新建文件…""添加仓库…"这几个之前先 `tt.Move` 到侧栏里
  （或 `a.FocusTree = true`）再 `Frame()` 两次（`Hovered` 看上一帧的指针）。
- 筛选：
  - 对保留原始大小写的路径评分。`fuzzy` 自己比的时候转小写，词首（小写后的大写）要看原样，别先 `ToLower` 再匹配（`TreeSide` 就不加分了）。
  - 仓库根下的文件 `name` 是 0：文件树用 `MatchPath` / `ScorePath`（把 0 当成整条都是名字）；`Match` 把 0 当没有名字，是给命令面板的。
  - 不要保存全部匹配结果：5 万个文件敲一个 `t` 几乎全匹配上，每个都造 `Row`、记命中的字、排一遍要几 MB、几十毫秒。`Tree.Match` 用 yield 只交出路径和分，
    调用方用 500 个的堆（`topHits`）留最好的，显示的那些再 `HitRow` 算命中的字。打分用不分配的 `ScorePath`。
- 大纲：Go 用 `go/parser.ParseFile`，有语法错误时它也返回解析出来的那部分 AST（不是 nil），照样列，别因为 `err != nil` 就丢掉。
  大纲只在侧栏是大纲模式、或命令面板是 `@` 时才算（`Doc.Outline` 缓冲区版本没变就用上次的），别在每帧对所有标签算。

## 编码和搜索

- `search.Match` 的 `Len` 只到这一行的行尾，`Span` 才是整个匹配：跨行的正则（`a\nb`）`Len` 比 `Span` 短。选中、标出来用 `Len`，替换、核对还在不在原位用 `Span`；
  别拿 `Col + Len` 去切内容。
- GB18030 的解码器能接受多种双字节组合：Shift-JIS、EUC-KR 的文件按 GB18030 也"解得通"（没有替换字符，只是乱码），所以 `pickCJK` 不能只看解码是否成功，还需按字符比例选择。
  阈值在 `internal/docs/encoding.go`，按语料调定：样本取前 64 KB；Shift-JIS 看全角假名占比，EUC-KR 看谚文和常用音节占比，Big5 看第二字节落在 40–7E 的比例，
  无 BOM 的 UTF-16 看一边 NUL 多、一边几乎没有。反过来，Big5 的码落进 GB18030 的用户区会解成替换字符，Big5 文件按 GB18030 是 `Broken`。
  - Shift-JIS 只按全角假名算（`isKana` 只认 U+3040–U+30FF）：半角片假名（A1–DF 单字节）在 GB18030 里是双字节的首字节，按它解出来的也不是假名。
  - 修改阈值前先运行 `TestDetectCJK` / `TestDetectCorpus` / `TestDetectUTF16NoBOM`。汉字多的日文和很短的 Big5 认成 GB18030 是已知的，别为它们再加规则把中文认错。
  - 认编码时跳过挑不上的：`pickFrom(data, true)` 在 GB18030 解得通以后，`cannotWin` 只看字节（`big5Low`；`kanaMost` / `hangulMost` 是解得通时 `share` 的上限）
    就知道挑不上的不解（10 KB 的中文从 99 µs 降到 34 µs）。改阈值、`score` 或 `cjk` 的顺序先跑 `TestPickSkip` 和 `TestKanaHangulMost`。
- GB18030 并非所有字节都能无损往返编码：单独的 0x80 解成 €，€ 编回去是 A2 E3。所以 GB18030 能不能编辑要在后台 `Encode` 回去逐字节比一遍（`decode` 的 `check`），
  不一样的是 `Doc.Lossy`、只读。测试里造"存不回原样"的文件就用 0x80。`Encode` 返回 nil 是这个编码表示不了某个字，`write` 报错、不写。
- 旧编码能否编辑由 `Lossy` / `Broken` 决定，不取决于编码名称（`Writable`）：GB18030 / Big5 / Shift-JIS / EUC-KR / EUC-JP / Windows-1252 / ISO-8859-1 编得回原样就能编辑，
  没 BOM 的 UTF-16 还是只读。测"只读条"要用编不回的样本（结尾单独一个 0x80），别拿普通 Big5 当只读的。
- `Settings` 里有 map（`Formatters`），测试里比两份设置用 `reflect.DeepEqual`，别用 `==`。
- 搜索在解码后的文字上做，坐标就是缓冲区坐标：
  - `search.readFile` 先 `utf8.Valid`（UTF-8 不多分配），不过才 `docs.Sniff`（和打开文件同一套认编码），解出来的字符串用 `unsafe` 借成只读的 `[]byte`、搜完就丢；
    `Match.Enc` 记编码，认不出的照原字节搜、预览换成 U+FFFD。
  - 替换：开着的文档在 `Buf.String()` 上 `Plan` → `Buf.Edit`（干净的按 `d.Enc` 编回写盘）；没开的 `docs.Decode` 可编辑才换、`Encode` 写回（nil 报"编不回 %s"、不写）。
  - 手动换过编码的标签和搜索认的不一样时 `Plan` 核对不过，算 stale。细节见 [architecture.md](architecture.md)。
- 结果跟着改动更新只换"变了的文件"（`app/searchfollow.go`）：别为了跟着磁盘就整份重搜，也别动 `ver` 没变的文件。"全部替换？"问着时靠 `ver` 判断结果作废没有，
  无意义地更新版本会让用户点击的"替换"变成"结果变了，没换"。新加会改结果的路径（替换、撤销替换、跟着更新）都走 `splice`，保持 (仓库顺序, 路径) 的顺序。
- regexp2（`search/advanced.go`）：
  - 位置走字符串接口：`FindRunesMatch` 给 rune 下标，`FindStringMatch` 才给字节位置（`bytesString` 不拷贝地借 `[]byte`，用完之前别改它）。
  - `MatchTimeout` 管的是一次匹配调用，不是整个文件：超时当这个文件跳过（`Result.TimedOut`）。
  - 组号要 `OptionMaintainCaptureOrder()` 才和 RE2 一样按出现顺序；`$1` 的展开用 `expandTemplate`（`TestExpandTemplate` 和 `regexp.Expand` 对上）。
  - 只在 RE2 编不了而且 `advancedSyntax` 认出那几种写法时才换 regexp2，别让普通正则也走回溯引擎（慢，还会超时）。
- 改文字的外部命令走 `formatDoc`（`app/format.go`）：后台跑，回主线程时 `Buf` 和 `Version()` 都没变才套，用 `docs.LineEdits` 一步套上。
  无输出时按失败处理，避免清空文件，超时连进程组一起杀（`Setpgid` + `Kill(-pid)`）。

## 文案和 i18n

- 界面文案一律 `i18n.T("中文")` / `i18n.Tf("…%s…", x)`，别拼接翻译过的片段（英文语序不一样）。
- `TestCatalog` 只收带汉字的字面量。`"%s：%s"`、`"，"` 这种没有汉字的格式串不进 `zh-CN.json`，英文界面中就会保留全角标点。
  要拼就用带汉字的格式（`"已暂存：%s"`），或者用 `" · "` 这种两种语言都能用的分隔。"Git"、快捷键这类本来就不进表。
- `TestCatalog` 不许占位符带序号（`%[2]s`）：英文译文需保持与中文一致的参数顺序，据此调整语句。
- 每个中文字符串只对应一条英文译文。"变基""还原""拣选""合并"在文案表里是操作名（英文小写的 rebase / revert / cherry-pick / merge，横幅和队列标签用），
  按钮上要首字母大写的另起一个中文串（"开始变基""还原提交"），别改操作名的英文。
- 禁用菜单项的原因说明使用 `ui.withWhy`（中文全角括号、英文 ` (…)`，按 `i18n.Lang()`），原因本身走 `i18n.T`。悬停提示的快捷键括号用 `app.Tip`，别自己拼。
- mygo 自带的文案（查找条、跳到行、DiffView 折叠行、SearchField）要中文得 `import _ "github.com/egoist/mygo/locales/zh-Hans"`，
  再 `mygo.App.SetLanguages(i18n.Locale())`（在 `cmd/workbench`，换语言时 `OnSettings` 再调）。测试里用 `Tester.SetLocale(i18n.Locale())`（`newUI` 里已设），
  所以测试里找的 mygo 文字跟着当前语言，默认中文。
- `wui.SearchField` 不写 `Placeholder` 时占位是本地化的"搜索"。框是干什么的照样写进 Tooltip / Label。
- 改了语言的测试要 `t.Cleanup(func() { i18n.Set("") })`，否则后续测试会沿用英文环境。
- 按"今天"变格式的文案，测试期望用同一个函数算：`LocalStamp`（`app/localhist.go`）当天给 `14:32`、今年别的日子给 `11-01 14:32`、往年带年份。
  测试里写死某一天的时间戳又写死 `"14:32"`，跨过当天零点后测试就会失败。所有依赖实际时间的文案（`LocalStamp`、相对时间），期望值一律拿同一个函数拼。

## 内存和性能

- 应用空闲后，Go 堆不会立即缩减：一大批短命分配（搜索读上万个文件）之后应用闲着，没有新的分配触发 GC 阈值，垃圾要等 Go 每 2 分钟一次的强制 GC 才收，
  这期间全算在 footprint 里（"全开"时多 ~23 MiB）。这类一次读取大量数据、用完即释放的后台任务，应在完整结束后调用一次 `debug.FreeOSMemory()`
  （`SearchNow`、`loadWtInfo` 就是这样）。别在每帧或每次按键里调。
- 启动峰值（头几秒两百多 MiB）是 mygo Metal 渲染器的 drawable，和应用的数据无关（峰值时 Go 的堆和空闲时一样），排查时不要将这部分归因于应用数据。
  用 `footprint -p PID --sample 0.1 -f bytes` 逐类拆过：一个最大化窗口刚开时 IOSurface 3 块 drawable 共 ~87 MiB，加 graphics 的 unmapped ~55，第一帧后约 2.5 s 才还到 1 块。
  每开一个窗口这两样都再来一份，几个窗口开得比 ~2.5 s 近，峰值就叠起来。所以 `RestoreGap` 默认 2.5 s，不要直接缩短；修改前先按此方法重新测量两个窗口的启动峰值
  （由 `TestRestoreGapDefault` 检查默认值）。
- 帧内存：mygo 2 秒没有新帧才放掉窗口大小的帧内存。光标闪、动画、后台一直要帧的组件都会把它留住，量内存时有焦点 / 无焦点都要量。
- 窗口整个被别的窗口盖住时 mygo 停帧（`holdFrame`），只画第一帧，空闲内存低得不正常（63 MiB 左右）。这时测得的数据无法与其他情况直接比较，
  量内存时用固定的窗口位置（见 `AGENTS.md` 的"单实例、真窗口和量内存"）。

## Markdown 预览的渲染

- WebView 只能在 Content 窗口里建，不放元素就看不见（`internal/render`）：`win.NewWebView` 建出来不挂 `ui.WebView` 元素，就是后台跑页面，正好拿来渲染。
  页面的协议（`wbrender://`）要在建任何窗口之前 `mygo.Protocol.Handle`（main 里 `render.Register()`），晚了 WebKit 不认。
  `Page.EvalContext` 能在后台 goroutine 调，渲染器的 goroutine 就靠它，别在 UI 线程上等它。
- canvas 被污染后无法导出 PNG：SVG 里有 `foreignObject`（mermaid 默认的 `htmlLabels: true` 就是）画到 canvas 后 `toDataURL` 抛 SecurityError。
  `page.html` 里 mermaid 必须 `htmlLabels: false`（含 `flowchart.htmlLabels`），改 mermaid 配置时别打开。
- MathJax 的错误是 `TexError`（不是 `Error`，`EvalError` 里只看到 `[object Object]`），要包成 `Error` 再抛。它的 SVG bbox 贴着字，四周需要留白，否则绘制结果会被裁剪。
- mygo 的段落只在内容变时才再问 `InlineMath`：行内公式第一次没好、按原文排了版，好了之后段落不会自己重排。`mdRender` 返回 `Renderer.Gen()`，
  预览拿它当 key 的一部分，一批好了换 key。`parser.ExtDollarMath` 不在 `parser.Default` 里，要自己或上才认 `$…$`。

## 测试和 Tester

测试分工和测量方法见 [testing.md](testing.md)。本节列出编写测试时的注意事项。

驱动与帧处理：

- `ui.NewTester` 一建好就画第一帧，那一帧用的是 state.json 里已有的值（没有就是 `Default`：三个区域都开）。测试要从"底栏关着"之类的状态起步，
  得在 `newUI` 之前把 state.json 写好（`terms_test.go` 的 `termState` / `setBar`），不能建好再改 `a.St`，第一帧已经把终端起了。
- Tester 一帧一帧地走：`tt.Advance(time.Second/60)` 最多画一帧，`settle`（`Frame()`）会连着画到 20 帧。后台通过 `a.Update` 返回的结果，需要在保留的 `*wui.Context` 上
  `c.Invalidate()` 才排下一帧（`TestMeasureBigFile` 的 `measureUI`）。
- `OnTextKey` 的回调在两帧之间跑：里面改状态走 `a.Update`，测试里要 `wait` 跑掉队列才看得到，仅调用 `tt.Frame()` 不够。
- `Element` 只在建它的那一帧有效，留到下一帧调 `.Bounds()` 会 panic（"element from pass N used in pass N"），还会中断整个包的测试。
  跨帧量位置用 `wui.Handle`：`.Bind(&h)`，帧外 `h.Bounds(c)`（`c` 是建帧时的 `Context`）。
- 同一行连着点两次之间要 `tt.Advance(time.Second)`，否则会被识别为双击。
- `a.Ask` 之后要 `tt.Frame()` 才查得到确认框的字：`Confirm` 是下一帧才画出来的浮层。
- 弹着的对话框挡住 Tester 的键：`AlertOpen` / 确认卡开着时 `tt.Key` 送不到下面。关掉（`a.AlertOpen = false` 或点按钮）之后先 `tt.Frame()` 一帧再送键。
- toast 会被下一帧画掉（`a.Toast` 清空）：UI 测试里查提示用 `tt.HasText`，在 app 包的测试里才直接比 `a.Toast`。
- `app.New` 只读状态、不开上次的标签，真窗口里 `Start` 是 `Windows.Open`（`app/windows.go`）调的。测会话来回时重开 App 后要自己 `a.Start()`，
  否则标签列表为空，看起来就像未保存会话。
- 设置窗口是第二个 `mygo.Window`，和主窗口共用一个 `App`；测试里用第二个 `wui.NewTester(SettingsView(a), …)`，改了设置后主窗口的 Tester 要 `Frame()` 一次才看得到。
- `view.files`（⇧⌘E）是切换侧栏，侧栏已经是文件页时再调会把侧栏关掉。测试里要确定打开文件页用 `a.ShowSidebar(workspace.ModeFiles)` + `a.EnsureFiles(r)`。
- `mygo.App.SetMenu` 不能在 `go test` 里调。Tester 的 `Menu()` 不包含禁用状态；检查菜单项是否禁用时，用 `ChooseMenuItem` 选择该项，确认错误中包含 "disabled"，再调用 `CloseMenu()`。

查找元素：

- Tester 的 `Label` / 文字不能重名，否则 `tt.Click` 可能定位错误：标签行用 `"标签 "+名`，编辑器用 `"编辑 "+名`。
- `Find` 按树的顺序返回第一个匹配（标题栏 → 侧栏 → 主区 → 底栏 → 右栏 → 状态栏 → 对话框），同一文字出现在两处时用唯一的 Label 定位。
- `Find` 是整段文字完全相等才算，`HasText` 是包含。找"2 条贮藏"这类拼出来的、或范围胶囊上"demo/docs"这类短字用 `Find`；
  结果行的 Label 带着路径（"结果文件 demo/docs/b.md"），`HasText` 会配到它。
- 给 `Text` 加了 `.Label` 之后，`Find` / `Texts` 看到的是 Label，不是显示的字。要按显示的字找就别给文字设 Label。
- 有 Label 的行（树行、历史行）`Find` 拿到的是整行的矩形（树行从 x=4 起），不是名字那段字。量位置用只出现一次的字（短哈希）定 X，`Find(标题).Y` 是行顶；
  量名字、图标就用 `inkRuns` 扫像素。`Find` 的矩形是 DIP，`Image()` 的像素要乘 `SetScale` 的倍数。`TreeSpans` 是上一帧的位置，改了滚动 / 行以后多 `Frame()` 几次再看吸顶栈。
- `wui.Toggle`（胶囊开关）在无障碍树里是切换按钮（`roleToggleButton` 31，按下 = 开），不是 Switch（6）：`accessTree` 里按角色找开关要用 31。
- `TestAccessLabels` 用 reflect 读 Tester 没导出的字段，所以 mygo 锁版本。升级 mygo 时会先由 `TestAccessProbe` 报错，按报错更新字段名。

像素测量：

- 软背景是渐变，隔几个 pt 的两个像素本来就差 1、2。按颜色比较（参考线、悬停底色、文字墨迹）都用 `inkDist` 加容差，别用 `==`。
- 彩色底块不算墨迹：底块和底色只差 30–80，`inkRuns`（门槛 90）只量到上面的字母 / 图标。量块用 `inkRunsOver(…, 20)`，只扫名字前面那一截（阈值过低时，缩进参考线也会被识别为色块）。
  要量文件树的彩色徽章先 `a.St.Settings.TreeIconsPlain = true` + `SettingsChanged()` + `Frame()` 两次。
- 量像素的测试（`TestCardGeometry`）要 `Backdrop: "flat"`、深色（纯色底、没有阴影）才干净，柔和的色斑会染缝。Tester 默认不通透，根是窗口底色，默认的柔和底在所有 UI 测试里都画着。
- Tester `SetVibrancy(true)` 时材质那块是透明像素（和 `WORKBENCH_CAPTURE` 一样）。截图测试用 `writeMaterialShot` 铺一块合成的毛玻璃，只是近似，真窗口透出来的样子要手测。
- 原生菜单（右键、按钮的 `Menu`）不在 `tt.Image()` / `WORKBENCH_CAPTURE` 里，测试用 `tt.Menu()`、`tt.ChooseMenuItem(…)`、`tt.CloseMenu()`。
  `WORKBENCH_CAPTURE` 的图里也没有红绿灯。
- 系统右键菜单（`ContextMenu`）在窗口靠下的地方会折起来（issue #6）：mygo 的 darwin 后端用 `popUpMenuPositioningItem:nil atLocation:inView:` 弹（v0.7.4 到 main `6121009b` 都是，`internal/darwin/menu.go` 第一个参数还是 0），
  左上角钉在点的地方，下面（到屏幕底 / Dock）放不下时 AppKit 不往上翻，而是把菜单截短、底下出滚动箭头。Holt 碰不到这个调用（位置也改不了），
  所以文件树的右键菜单（`treeMenu`、`rowMenu`、仓库行、钉在顶上的行、压缩目录的每一段）画在窗口里（先是 `PopoverMenu`，issue #48 起是 Holt 自己画的
  `ui/gmenu.go`），下面放不下就往上开，窗口本身都放不下才滚。代价是样子跟着主题、不是系统菜单的毛玻璃。别处的右键菜单还是系统的。
  画出来的菜单 Tester 的 `Menu()` / `ChooseMenuItem` 看不见：测试统一用 `popmenu_test.go` 的 `shownMenu` / `chooseMenu` / `closeMenu`
  （两种都认，画出来的从无障碍树里找 RoleMenu / RoleMenuItem，所以 `gmenu.go` 的面板和项要设 `Role`、`Label`、灰项 `Disabled`），`mustRightClick` 已经换成它们了。
- `PopoverMenu`（v0.7.4；main `6121009b` 的 `SetMenuPresentation(PopoverMenus)` 也是它，这几条照旧）在 macOS 上右键按下就开（`menuPress`），开的时候没有元素被"按着"，所以按住右键移动时项的悬停高亮跟着走；
  但松开右键不会选中指针下的项（项只认左键的 `Clicked`），子菜单悬停立刻开、没有安全三角，灰掉的项没有提示，点外面关掉的那一下照样落到下面的元素上。
  这些要自己画菜单才做得到（issue #46），所以文件树换成了 Holt 自己画的（issue #48，`app/popmenu.go` + `ui/gmenu.go`）。
  Tester 只有左键的 `Press` / `Release`，右键只有 `RightClickAt`（按下松开一起），按住右键拖测不了：菜单的状态机放在 `app.PopMenu` 里直接喂事件测。
- 自己画菜单时的几处（issue #48）：右键按下由行的 `HandleInput` 接，接了以后按着的移动和松开都送到这一行（不管指针在哪），所以按住拖松开不用别的；
  一个元素只有一个 `HandleInput`，原来挂着的（`treePress`、列表的 `treeCommands`）要包进去，不是右键的照旧转给它。`HandleInput` 让出去的按下
  不会再给外层元素的 `HandleInput`（`rt.handler` 只取最里面那个），所以列表的空白处菜单只在没挂 `HandleInput` 的地方开。
  吞掉点外面那一下靠 Overlay 里铺满窗口的接收层（`Absolute` + 窗口大小 + `HandleInput` 接下按下）；它 `Focus()` 每帧拿着焦点接键盘，
  `Element.Focused()` 在窗口失去焦点时报假（mygo 的 `windowFocus`），拿它当"失去焦点就关"。回调里的坐标是相对元素的，换成窗口坐标用建界面时记下的
  `Bounds()`（上一帧的），回调里不能再碰元素。
  Overlay 关掉时 mygo 把键盘还给它出现那一帧有键盘的元素（`openOverlay` / `restoreFocus`），但记的是 Overlay 的 `fn` 跑完以后的焦点：
  出现那一帧里面的元素就 `Focus()` 了，记下的就是它自己，关掉以后键盘没了着落。接收层要隔一帧再 `Focus()`。
- mygo main（`6842ad5e` 起）的 `Tester.Click` / `RightClick` 先校验：标签所在元素（往上数第一个接输入的）要和点下去接输入的是同一个，
  不是就报 `"xxx" is not the input target`。列表的行名（`ListOptions.Label`，打字跳行、读屏用）记在 List 的那一格上，接点击的却是格子里面的行
  （文件树行的 `menuPress`、Git 栏段头、命令面板的结果行、历史行）；历史右栏标题行的名字"历史"在整行上，正中是搜索框。真窗口里点那一格就是点那一行，
  不是回归。测试里点这种行用 `clickrow_test.go` 的 `clickRow` / `rightClickRow` / `mustClickRow` / `mustRightClickRow`（按名字找到那一格、点正中），
  别的照旧用 `Click`，保留它的校验。
- 同一版起所有按键都进 `HandleInput`（`d3288030`）：Holt 的几个回调（`treeCommands` 只认 `InputCommand`、菜单接收层放过 ⌘ / ⌃、看图只认不带修饰的 = - 0、
  录键卡片本来就要全接）都按种类和键筛过，不多接也不吞键。
  接收层的回调返回 false 也不会把按下让给它底下的元素（`hitChain` 只往这一层的祖先走，到不了 Overlay 外面），所以"开着时右键别处开新菜单"
  得自己按登记的位置找（`PopMenu.AddTarget` / `TargetAt`）；文件树以外的地方右键只关菜单。
  ⇧F10 / 菜单键：mygo 只给挂了 `ContextMenu` / `PopoverMenu` 的元素开菜单（`menuKey`），去掉 `PopoverMenu` 以后它们就没用了，
  要在有焦点的元素的 `HandleInput` 里自己接（`InputKeyDown` 先送给它，接了 mygo 就不再找菜单）。
- 列表行的按下（issue #46）：`ListState.DragSelecting()` 只在按着拖远了以后才报真，不能拿来判断"还按着"；列表在按下时就选中没选中的行，
  按下和松开对应用分不开。要按下选中、松开打开，就让行自己用 `HandleInput` 接 `InputPointerDown`（接了以后移动和松开都归它），
  而且松开之前每帧都要接着挂 `HandleInput`（不挂，状态上的回调就没了，松开送不到）。回调里不能用建界面时的 `wui.Element`（会 panic
  "element from pass … used in pass …"），要位置用上一帧记下的矩形。挂了 `HandleInput` 的元素，即使回调把按下让出去，mygo 数双击也按它数
  （`countPress` 取 handler 和 target 里靠里的那个），⇧点之后马上单击同一行会被数成双击；测试里两次点之间 `Advance` 一下。
- 无头 Tester 每帧都软件画整窗，一帧的底约 1.8 ms，量出来的帧时间比真窗口的高。后台高亮追色的帧一帧 0.1 ms，Tester 连着画，1800 帧不到 1 s 就走完了，
  后台还没算完。所以量时间别数帧：`TestMeasureBigFile` 量的是墙上时间（1800 帧和 30 s 都过了才停），"几帧完成"只对照 60 Hz 的真窗口看。

终端、搜索、监听：

- 终端启动后立即调用 `tt.Type` 会在提示符出来前被 shell 回显成一行"预输入"：先等提示符（`testutil.WaitTermReady`）再敲。
- 测 ⌘点击终端里的链接：`tt.HoldModifiers(wui.Cmd)` + `tt.Move` 竖着扫，`Terminal.HoveredLink()` 给出链接和相对视图的矩形，再 `tt.ClickAtWith(wui.Cmd, x, y)`；
  网址看 `tt.OpenedURLs()`（只记录，不实际打开）。
- 测试里的 termd 套接字在 `t.TempDir()`：`termd.Server` 用 `Listen` / `Serve` 在测试进程里跑，`t.Cleanup(srv.Stop)`。macOS 的 unix 套接字路径上限 104 字节，
  `t.TempDir()` 很长，用 `SockPath(t.TempDir())`（太长时换到 `$TMPDIR` 下的短路径），`t.Cleanup` 里连 `.lock` 一起删。
  界面测试把 `app.TermdStart` 换掉（不要实际启动子进程），只有 `TestSpawnDetached` 起真的子进程，最后关掉。
- 输入之后等搜索要等内容，别只等 `Done`：打字后有 150 ms 的去抖，那段时间 `Done && !Running` 还是上一次搜完的样子，`wait` 立刻就过了。
  等结果里的处数和 `Preview` 里有这个词（`searchbits_test.go` 的 `searched`）。
- 测"不该报"之前先等一个探针报了（`internal/watch` 测试的 `probe`）：FSEvents 按顺序给，建仓库、提交、切分支的事件在负载重时会晚到几百毫秒，
  只静等一段时间会让它们落进"不该报"的窗口里。先改一个会报的文件、等它报了（之前的都到了）再静等。
- "没有多做一次"这类断言要防别的监听插进来：在工作树里跑的 `git status` 会顺手重写 `.git/worktrees/<名字>/index`，主仓库的监听晚到一个 Git 变化就整块重算一次。
  断言"只重算了那一条 / 计数没变"之前先等稳（仓库都闲着、没有整块在算）再记基准；碰上了换个新输入重试几次，断言至少一次走到了那条路（`app/wtwatch_test.go`）。

避免影响开发机：

- 测试里不调 `copyText` 或任何剪贴板动作，这些操作会修改测试机器的系统剪贴板。网页用 `a.OpenURL`、访达 / 打开目录用 `a.ShowPath`，换成只记不开的函数
  （`newUI` 默认把两个都设成什么都不做）。
- 测试和截图里跑 `git log` / `git diff` 要加 `--no-pager`：终端里的 git 会开 less，画面停在 `(END)`。
- 测试里本地路径的子模块要 `protocol.file.allow=always`（git 2.38 起默认不让从本机路径克隆子模块）。`testutil.SubmoduleRepo` 自己带 `-c`；
  走应用 `RunOp` 的初始化在测试里先 `t.Setenv("GIT_CONFIG_COUNT", "1")` 和 `GIT_CONFIG_KEY_0` / `GIT_CONFIG_VALUE_0`。应用里不加这个配置。
- 测试中不运行实际的检查 / 格式化工具：go vet、tsc、cargo 会写 GOCACHE / `target/`、慢，机器上也不一定装。命令一律用 shell 一行（`printf '…'; exit 1`）
  或 `t.TempDir()` 里写的假脚本（截图要像真的命令名时，写叫 go / tsc 的 `#!/bin/sh` 脚本），`Settings.Shell` 设成 `/bin/sh`，测超时的时候把 `LintTimeout` 调短。

真窗口和原生拖放：

- Tester 进不了原生拖放：`DropData` 只在 AppKit 的拖动会话里有，测试里直接调 `App.DropOnDoc` / `DropOnTerm`。界面那层在构建出来的 .app 里试：
  一个小的 Swift `NSDraggingSource` 窗口当拖动源（文字 / file URL / file URL + 文件名文字，后一种模仿访达），CGEvent 按下 → 拖 → 松开。这类脚本不在仓库里。
  别用 AppleScript 控制访达：会超时（-1712），还可能弹授权框。
- 有未保存的标签时，TERM 无法直接退出应用：应用在问"保存吗"，进程还在，下一次真实窗口测试可能把参数交给该实例。自己起的实例先确认 `WORKBENCH_STATE` 是临时目录，再 `kill -9` 它自己的 pid；
  不要按进程名终止，以免影响日常使用的 Holt。
- ad hoc 签名的新构建读可移除的卷会卡住：macOS 对每个新签名的应用，第一次读可移除的卷（外接盘之类）都要过一次授权，无人确认授权时会阻塞在 `open()` 中（`sample` 看得到），
  窗口也不出来。量内存、跑临时构建时工作区对着内置盘上的一份克隆，别对着外接卷上的仓库。

杂项：

- `TestScreenshot` 的输出目录要先建。
- 注释里别写两个连着的单引号：gofmt 会把 `''` 改成弯引号（`'\''` 变成 `'\”`）。
- 新建测试文件前先看有没有同名的：用 shell 重定向（`cat > …_test.go`）会直接覆盖已有测试。写新文件用编辑工具的"新建"（已有的会报错），或先 `ls`。
- zsh 里 `echo ====` 会报错（`=` 开头是文件名展开），要加引号。
