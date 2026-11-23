# 听澜界面预览

这里展示当前客户端截图，也保留早期设计参考。GTK4 / libadwaita 界面源码位于 `native/cmd/music/designui/`，已嵌入 Linux 可执行文件。

```sh
./out/native/tinglan
```

上述命令在仓库根执行，也可在任意目录用可执行文件的绝对路径启动。桌面运行时需要 GTK4、libadwaita（至少 1.6）、系统 Python 3 和 PyGObject。默认跟随系统明暗，可用 `--dark` / `--light` 复查，以及 `--width 400 --height 680` 看窄窗；`--page lyrics` 或 `--page queue` 直达预览状态。

## 当前客户端截图

| 页面 | 截图 |
| --- | --- |
| 播放页 | [显示上下栏](screenshots/current/lyrics.png) / [隐藏上下栏](screenshots/current/lyrics-immersive.png) |
| 我的收藏 | [收藏列表](screenshots/current/favorites.png) |
| 搜索 | [搜索页](screenshots/current/search.png) |
| 我的歌单 | [歌单列表](screenshots/current/playlists.png) |
| 设置 | [播放设置](screenshots/current/playback-settings.png) |

## 早期设计参考 · 2026-11-20

| 场景 | 截图 |
| --- | --- |
| 全高侧栏与底部曲目信息 | [深色](screenshots/sidebar-dark-1140.png) / [浅色](screenshots/sidebar-light-1140.png) |
| 收藏 · 宽窗 | [1140 × 760](screenshots/favorites-1140.png) |
| 队列 · 宽窗 | [1140 × 760](screenshots/queue-1140.png) |
| 歌词 · 深色 | [1140 × 760](screenshots/lyrics-1140.png) / [1920 × 1080](screenshots/lyrics-1920.png) |
| 歌单 | [1140 × 760](screenshots/playlists-1140.png) / [400 × 680](screenshots/playlists-400.png) |
| 歌单 · 悬浮 | [1140 × 760](screenshots/playlists-hover-1140.png) |
| 收藏 · 窄窗 | [400 × 680](screenshots/favorites-400.png) |
| 音源设置 · 窄窗 | [400 × 680](screenshots/settings-400.png) |

## 界面取舍

- 收藏是操作面，不是介绍页。只有页名、实际曲目数和操作；不放标语、英文眉题或解释性副标题。曲名、艺人、时长和音源状态是数据，不是文案装饰。
- 用稳定的底部播放条跨页保持上下文。宽窗队列与内容并排，窄窗队列覆盖呈现；歌词独占内容区，时间调整和文件导入归到标题栏。
- 原型能切页、选歌、调整预览进度、搜索示例曲目、展开队列和进出歌词。导入、登录、设置写入等仅有布局或交互占位。不要从原型控件推断功能已经接通或可以删掉。

## 正式迁移的功能对照

| 现有来源 | 正式 GTK 实现不得遗漏 |
| --- | --- |
| `native/cmd/music/shell.go`、`details.go` | 收藏总数与分页、导入、播放全部；搜索分页、按音源切换和点歌入队；曲目收藏及专辑/艺人详情 |
| `panels.go`、`dialogs.go`、`writes.go` | 本地与导入歌单、创建/添加/删除/更新、只读镜像及确认、播放历史 |
| `transport.go`、`transport_menu.go`、`stage.go` | 播放与拖动定位、试听、音质、音量、播放模式、选曲/移除/清空队列、沉浸歌词及其时序、封面收起、歌词导入与偏移 |
| `settings.go`、`sources.go`、`application.go` | 原有全部设置项、音源启停与登录、ZIP/目录添加、托盘行为、MPRIS 与单实例。显示方式可换，行为保留。 |

原型不用于评估万首曲库性能；正式实现需换成回收列表项的 GTK 列表模型，并按迁移计划测量。
