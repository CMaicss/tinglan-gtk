#!/usr/bin/env python3
"""Tinglan GTK interface. Without a Go backend it runs in offline preview mode."""
import argparse
from pathlib import Path
from weakref import WeakSet
import gi

gi.require_version('Gtk', '4.0')
gi.require_version('Adw', '1')
from gi.repository import Adw, Gdk, Gio, GLib, Graphene, Gtk, Pango

UI_DIR = Path(__file__).resolve().parent


def asset(name):
    bundled = UI_DIR / name
    if bundled.is_file():
        return bundled
    if name == "icon.png":
        return UI_DIR.parents[3] / "build" / name
    return UI_DIR.parent / name


THEME_OPTIONS = ['跟随系统', '亮色', '暗色']

PAGES = {
    'favorites': ('我的收藏', 'emblem-favorite-symbolic'),
    'playlists': ('我的歌单', 'view-list-symbolic'),
    'search': ('搜索', 'system-search-symbolic'),
    'history': ('播放历史', 'document-open-recent-symbolic'),
    'settings': ('设置', 'emblem-system-symbolic'),
}
TRACKS = [
    ('潮汐来信', '迟岛', '海岸线', '4:26'), ('落日之后', '周北', '日落之前', '3:48'),
    ('城市微光', '南线列车', '夜间列车', '4:12'), ('无人海岸', '灰鲸', '慢浪', '5:06'),
    ('春日漫游', '又青', '春游', '3:34'), ('最后一班船', '迟岛', '海岸线', '4:40'),
    ('山的那边', '纸树', '群山', '3:28'), ('过云雨', '林屿', '沿途', '3:56'),
    ('凌晨四点', '周北', '日落之前', '4:05'), ('雾中散步', '灰鲸', '慢浪', '3:51'),
]
PALETTES = [('173f47','84ad99','e7c186'), ('8e4736','dc9770','f6dab0'), ('252f49','697eab','e8c0aa'),
            ('294955','829da7','eddfbf'), ('526249','c2bf85','f2d5a5'), ('433c54','9f8199','e9c3aa')]
CSS = b'''
.sidebar-title { font-weight: 750; }
.sidebar-list { background: transparent; }
.sidebar-list row { border-radius: 8px; margin: 2px 0; }
.page-title { font-weight: 800; font-size: 30px; }
.art { border-radius: 6px; }
.album-art { border-radius: 12px; }
.cover-button { padding: 0; }
.player { background: @view_bg_color; }
.stage-glass, .stage-glass headerbar, .stage-glass .player { background: transparent; box-shadow: none; }
.stage-glass .stage-foreground { color: #fff; }
.stage-glass .stage-foreground headerbar button.flat, .stage-glass .player button.flat { color: #fff; }
.stage-glass .player scale trough { background: rgba(255,255,255,.25); }
.stage-glass .player scale highlight { background: rgba(255,255,255,.82); }
.player button.transport-primary { min-width: 44px; min-height: 44px; padding: 0; border-width: 0; border-radius: 999px; }
.app-brand { font-weight: 750; }
.tinglan-sidebar { background: @sidebar_bg_color; color: @sidebar_fg_color; }
.player scale { padding: 0; }
.player scale trough { min-height: 3px; }
.lyric { font-size: 23px; font-weight: 670; }
.lyric.dim-label { opacity: .48; }
.lyric.active { font-weight: 850; }
'''


def box(vertical=False, spacing=0):
    return Gtk.Box(orientation=Gtk.Orientation.VERTICAL if vertical else Gtk.Orientation.HORIZONTAL, spacing=spacing)


def margin(widget, x, y):
    widget.set_margin_start(x)
    widget.set_margin_end(x)
    widget.set_margin_top(y)
    widget.set_margin_bottom(y)
    return widget


def label(text='', style=None):
    widget = Gtk.Label(label=text, xalign=0, ellipsize=Pango.EllipsizeMode.END)
    if style:
        widget.add_css_class(style)
    return widget


def icon(name, size=16):
    obj = Gtk.Image.new_from_icon_name(name)
    obj.set_pixel_size(size)
    return obj


def button(text, glyph=None, action=None, style=None):
    if glyph:
        obj = Gtk.Button(icon_name=glyph, tooltip_text=text)
        obj.update_property([Gtk.AccessibleProperty.LABEL], [text])
    else:
        obj = Gtk.Button(label=text)
    if style:
        obj.add_css_class(style)
    if action:
        obj.connect('clicked', lambda *_: action())
    return obj


def scroll(child):
    s = Gtk.ScrolledWindow(hscrollbar_policy=Gtk.PolicyType.NEVER, vexpand=True, hexpand=True)
    s.set_child(child)
    return s


def clamp(child, size=920):
    return Adw.Clamp(maximum_size=size, tightening_threshold=max(320, size - 180), child=child)


def menu(title, entries, glyph='view-more-symbolic'):
    pop = Gtk.Popover()
    content = margin(box(True, 2), 4, 4)
    for name, cb in entries:
        b = button(name, action=None, style='flat')
        b.get_child().set_xalign(0)
        b.connect('clicked', lambda _, f=cb: (pop.popdown(), f() if f else None))
        content.append(b)
    pop.set_child(content)
    return Gtk.MenuButton(icon_name=glyph, tooltip_text=title, popover=pop, css_classes=['flat'])


def cover(n, size, rounded=False):
    dark, mid, light = PALETTES[n % len(PALETTES)]
    cx = 65 + n % 5 * 24
    clip = ('<defs><clipPath id="edges"><rect width="240" height="240" rx="19"/></clipPath></defs>'
            '<g clip-path="url(#edges)">') if rounded else '<g>'
    svg = f'''<svg xmlns="http://www.w3.org/2000/svg" width="240" height="240" viewBox="0 0 240 240">{clip}
    <rect width="240" height="240" fill="#{dark}"/><circle cx="{cx}" cy="79" r="37" fill="#{light}"/>
    <path d="M0 132 Q49 101 105 145 T240 116 V240 H0Z" fill="#{mid}"/>
    <path d="M0 176 Q61 134 122 173 T240 147 V240 H0Z" fill="#{dark}" opacity=".55"/>
    <path d="M0 206 Q56 162 122 201 T240 166 M0 220 Q56 176 122 215 T240 180"
    fill="none" stroke="#{light}" opacity=".48" stroke-width="1"/></g></svg>'''.encode()
    art = Gtk.Image.new_from_paintable(Gdk.Texture.new_from_bytes(GLib.Bytes.new(svg)))
    art.set_pixel_size(size)
    art.set_size_request(size, size)
    art.set_overflow(Gtk.Overflow.HIDDEN)
    art.add_css_class('album-art' if size > 80 else 'art')
    return art


class StageBackdrop(Gtk.Widget):
    radius = 36
    artwork_grace_ms = 350

    def __init__(self):
        super().__init__(can_target=False, hexpand=True, vexpand=True, visible=False)
        self.texture = None
        self.outgoing = None
        self.progress = 1.0
        self.animation = None
        self.faces = WeakSet()

    @classmethod
    def cover_rect(cls, width, height, image_width, image_height):
        padding = cls.radius * 2
        scale = max((width + 2 * padding) / image_width, (height + 2 * padding) / image_height)
        w, h = image_width * scale, image_height * scale
        return (width - w) / 2, (height - h) / 2, w, h

    def repaint(self):
        self.queue_draw()
        for face in self.faces:
            face.queue_draw()

    def advance(self, progress):
        self.progress = progress
        if progress >= 1:
            self.outgoing = None
        self.repaint()

    def set_texture(self, texture):
        if self.texture is texture:
            return
        old = self.outgoing if self.outgoing is not None and self.progress < .5 else self.texture
        if self.animation is not None:
            self.animation.pause()
            self.animation = None
        self.texture = texture
        if (old is not None and texture is not None and self.get_mapped()
                and self.get_settings().get_property('gtk-enable-animations')):
            self.outgoing = old
            self.advance(0)
            target = Adw.CallbackAnimationTarget.new(self.advance)
            self.animation = Adw.TimedAnimation.new(self, 0, 1, 230, target)
            self.animation.play()
        else:
            self.advance(1)

    def paint_pair(self, snapshot, draw):
        if self.outgoing is None:
            draw(self.texture)
            return
        snapshot.push_cross_fade(self.progress)
        draw(self.outgoing)
        snapshot.pop()
        draw(self.texture)
        snapshot.pop()

    def do_snapshot(self, snapshot):
        texture = self.texture
        width, height = self.get_width(), self.get_height()
        if texture is None or min(width, height) <= 0:
            return
        viewport = Graphene.Rect().init(0, 0, width, height)
        snapshot.push_clip(viewport)
        snapshot.push_blur(self.radius)
        luminance = Graphene.Matrix().init_from_float([
            .2126, .2126, .2126, 0,
            .7152, .7152, .7152, 0,
            .0722, .0722, .0722, 0,
            0, 0, 0, 1,
        ])
        snapshot.push_color_matrix(luminance, Graphene.Vec4().init(0, 0, 0, 0))
        def draw(image):
            x, y, w, h = self.cover_rect(width, height, image.get_width(), image.get_height())
            snapshot.append_texture(image, Graphene.Rect().init(x, y, w, h))
        self.paint_pair(snapshot, draw)
        snapshot.pop()
        snapshot.pop()
        shade = Gdk.RGBA()
        shade.parse('rgba(12, 12, 12, 0.60)')
        snapshot.append_color(shade, viewport)
        snapshot.pop()


class StageCoverFace(Gtk.Widget):
    def __init__(self, transition):
        super().__init__(can_target=False, hexpand=True, vexpand=True)
        self.transition = transition
        transition.faces.add(self)

    def do_snapshot(self, snapshot):
        if self.transition.texture is None:
            return
        width, height = self.get_width(), self.get_height()
        viewport = Graphene.Rect().init(0, 0, width, height)
        snapshot.push_clip(viewport)
        def draw(texture):
            scale = max(width / texture.get_width(), height / texture.get_height())
            w, h = scale * texture.get_width(), scale * texture.get_height()
            snapshot.append_texture(texture, Graphene.Rect().init((width-w)/2, (height-h)/2, w, h))
        self.transition.paint_pair(snapshot, draw)
        snapshot.pop()


class Preview:
    live = False
    art = staticmethod(cover)

    def __init__(self, app, args):
        self.args = args
        self.current, self.playing = 0, True
        self.queue = [0, 1, 2, 3, 5]
        self.favorites = set(range(len(TRACKS)))
        self.liked_buttons, self.track_rows = [], []
        self.win = Adw.ApplicationWindow(application=app, title='听澜 · 界面预览', default_width=args.width, default_height=args.height)
        self.win.set_size_request(360, 480)
        self.root = box(True)
        self.win.set_content(self.root)
        self.nav_split = Adw.OverlaySplitView(min_sidebar_width=190, max_sidebar_width=208, sidebar_width_fraction=.19, vexpand=True)
        self.sidebar = self.navigation()
        self.nav_split.set_sidebar(self.sidebar)
        self.screen = Gtk.Stack(vexpand=True, transition_type=Gtk.StackTransitionType.CROSSFADE, transition_duration=130)
        self.header = Adw.HeaderBar(show_start_title_buttons=False)
        self.header.set_title_widget(Gtk.Box())
        self.header.add_css_class('flat')
        self.nav_toggle = button('导航', 'sidebar-show-symbolic', lambda: self.nav_split.set_show_sidebar(not self.nav_split.get_show_sidebar()), 'flat')
        self.header.pack_start(self.nav_toggle)
        self.back = button('返回', 'go-previous-symbolic', lambda: self.open(self.lyrics_from if self.view == 'lyrics' else self.return_page), 'flat')
        self.header.pack_start(self.back)
        self.compact_brand, _ = self.brand()
        self.header.pack_start(self.compact_brand)
        self.primary_menu = self.application_menu()
        self.header.pack_end(self.primary_menu)
        self.lyric_options = menu('歌词选项', [('导入歌词', lambda: self.dialog('导入歌词', [], '导入', field='歌词文件')),
            ('歌词提前半秒', None), ('歌词延后半秒', None)])
        self.header.pack_end(self.lyric_options)
        self.main_pane = box(True)
        self.main_pane.append(self.header)
        self.root.append(self.nav_split)
        self.queue_split = Adw.OverlaySplitView(content=self.screen, sidebar_position=Gtk.PackType.END,
                                                min_sidebar_width=267, max_sidebar_width=300, sidebar_width_fraction=.25,
                                                show_sidebar=False)
        self.queue_split.set_vexpand(True)
        self.main_pane.append(self.queue_split)
        self.stage_backdrop = StageBackdrop()
        self.stage_surface = Gtk.Overlay(child=self.main_pane)
        self.stage_surface.add_overlay(self.stage_backdrop)
        # Main content stays above a non-interactive, unmeasured background.
        self.stage_backdrop.insert_before(self.stage_surface, self.main_pane)
        self.stage_surface.set_measure_overlay(self.stage_backdrop, False)
        self.main_pane.add_css_class("stage-foreground")
        self.nav_split.set_content(self.stage_surface)
        self.queue_split.set_sidebar(self.queue_view())
        self.queue_split.connect('notify::show-sidebar', lambda *_: self.queue_state())
        self.screen.add_named(self.library(), 'favorites')
        self.screen.add_named(self.playlists(), 'playlists')
        self.screen.add_named(self.search(), 'search')
        history = self.page('播放历史')
        self.add_tracks(history[1], reversed(range(len(TRACKS))) if not self.live else (), False)
        self.screen.add_named(history[0], 'history')
        self.history_area = history[1]
        self.screen.add_named(self.settings(), 'settings')
        self.main_pane.append(Gtk.Separator())
        self.main_pane.append(self.player())
        self.narrow(1110, (self.queue_split, 'collapsed', True))
        self.narrow(720, (self.queue_split, 'collapsed', True),
                    (self.nav_split, 'collapsed', True), (self.nav_split, 'show-sidebar', False),
                    (self.nav_toggle, 'visible', True), (self.tools, 'visible', False), (self.more, 'visible', True),
                    (self.playlist_area, 'margin-start', 14), (self.playlist_area, 'margin-end', 14))
        self.nav_toggle.set_visible(False)
        self.more.set_visible(False)
        self.win.connect('notify::current-breakpoint', self.layout_for_width)
        self.open(args.page)
        if args.queue or args.page == 'queue':
            self.queue_split.set_show_sidebar(True)
        self.select(0, keep_position=True)

    def layout_for_width(self, *_):
        compact = self.nav_split.get_collapsed()
        self.layout_player(compact)
        self.layout_identity()
        self.layout_playlists(2 if compact else 3)

    def narrow(self, width, *setters):
        br = Adw.Breakpoint.new(Adw.BreakpointCondition.parse(f'max-width: {width}px'))
        self.win.add_breakpoint(br)
        for setter in setters:
            br.add_setter(*setter)

    def brand(self):
        brand = box(spacing=8)
        mark = Gtk.Image.new_from_file(str(asset('icon.png')))
        mark.set_pixel_size(24)
        brand.append(mark)
        brand.append(label('听澜', 'app-brand'))
        return brand, mark

    def navigation(self):
        sidebar = box(True)
        sidebar.add_css_class('tinglan-sidebar')
        brand, self.brand_icon = self.brand()
        brand.set_margin_start(26)
        brand.set_size_request(-1, 46)
        sidebar.append(brand)
        area = margin(box(True, 4), 12, 12)
        self.nav = Gtk.ListBox(css_classes=['sidebar-list', 'navigation-sidebar'])
        for key in ('search', 'favorites', 'playlists', 'history'):
            row = self.nav_row(key)
            self.nav.append(row)
        self.nav.connect('row-activated', lambda _, row: self.open(row.key))
        area.append(self.nav)
        sidebar.append(area)
        spacer = box()
        spacer.set_vexpand(True)
        sidebar.append(spacer)
        self.sidebar_track = margin(box(), 20, 20)
        sidebar.append(self.sidebar_track)
        return sidebar

    def application_menu(self):
        model = Gio.Menu()
        def refresh(*_):
            model.remove_all()
            model.append('退出全屏' if self.win.is_fullscreen() else '全屏', 'win.fullscreen')
            model.append('设置', 'win.preferences')
            model.append('关于听澜', 'win.about')
            if self.live:
                model.append('退出听澜', 'win.quit')
        for name, run in (
            ('fullscreen', lambda: self.win.unfullscreen() if self.win.is_fullscreen() else self.win.fullscreen()),
            ('preferences', lambda: self.open('settings')),
            ('about', self.about),
        ):
            command = Gio.SimpleAction.new(name, None)
            command.connect('activate', lambda *_, cb=run: cb())
            self.win.add_action(command)
        if self.live:
            quit_action = Gio.SimpleAction.new('quit', None)
            quit_action.connect('activate', lambda *_: self.quit_window())
            self.win.add_action(quit_action)
            self.win.get_application().set_accels_for_action('win.quit', ['<Primary>q'])
        self.win.get_application().set_accels_for_action('win.fullscreen', ['F11'])
        self.win.connect('notify::fullscreened', refresh)
        refresh()
        return Gtk.MenuButton(icon_name='open-menu-symbolic', tooltip_text='主菜单', menu_model=model, css_classes=['flat'])

    def about(self):
        self.about_dialog = Adw.AboutDialog(
            application_name='听澜',
            application_icon='tinglan-about-logo',
            developer_name='Maicss',
            comments='支持音源插件的 Linux 桌面音乐播放器，使用 Go、GTK4 和 libadwaita 构建。',
            license_type=Gtk.License.AGPL_3_0,
        )
        self.about_dialog.add_link('Fork 自 talongao/tinglan', 'https://github.com/talongao/tinglan')
        self.about_dialog.present(self.win)

    def nav_row(self, key):
        text, symbol = PAGES[key]
        row = Gtk.ListBoxRow()
        row.key = key
        line = margin(box(spacing=12), 10, 9)
        symbol = f'tinglan-nav-{"heart" if key == "favorites" else key}-symbolic'
        line.append(icon(symbol))
        line.append(label(text))
        row.set_child(line)
        return row

    def open(self, key):
        if key == 'queue':
            key = 'favorites'
        self.view = key
        self.lyric_options.set_visible(key == 'lyrics')
        self.nav_split.set_show_sidebar(key != 'lyrics' and not self.nav_split.get_collapsed())
        self.screen.set_visible_child_name(key)
        self.back.set_visible(key in ('lyrics', 'detail'))
        row = self.nav.get_first_child()
        while row:
            if row.key == key:
                self.nav.select_row(row)
                break
            row = row.get_next_sibling()
        if key == 'settings':
            self.nav.unselect_all()
        if self.nav_split.get_collapsed():
            self.nav_split.set_show_sidebar(False)
        self.layout_identity()
        self.refresh_stage_backdrop()

    def refresh_stage_backdrop(self):
        if self.view != 'lyrics' or self.current is None:
            self.set_stage_texture(None)
        elif (texture := self.stage_background_texture()) is not None:
            self.set_stage_texture(texture)
        elif self.stage_backdrop.texture is not None:
            if not self.stage_backdrop.get_settings().get_property('gtk-enable-animations'):
                self.set_stage_texture(None)
            elif not getattr(self, '_stage_grace_source', 0):
                # Repeated snapshots or rapid skips without ready artwork must
                # not extend the lifetime of the retained image.
                self._stage_grace_source = GLib.timeout_add(
                    self.stage_backdrop.artwork_grace_ms, self.expire_stage_texture)

    def expire_stage_texture(self):
        self._stage_grace_source = 0
        self.set_stage_texture(None)
        return GLib.SOURCE_REMOVE

    def set_stage_texture(self, texture):
        if source := getattr(self, '_stage_grace_source', 0):
            GLib.source_remove(source)
            self._stage_grace_source = 0
        self.stage_backdrop.set_texture(texture)
        available = texture is not None
        self.stage_backdrop.set_visible(available)
        if not available:
            self.stage_surface.remove_css_class("stage-glass")
        else:
            self.stage_surface.add_css_class("stage-glass")

    def stage_background_texture(self):
        n = self.current
        if getattr(self, "_preview_backdrop_track", None) != n:
            self._preview_backdrop_track = n
            self._preview_backdrop_texture = cover(n, 240).get_paintable()
        return self._preview_backdrop_texture

    def page(self, name):
        content = margin(box(True, 16), 24, 12)
        heading = box(spacing=12)
        title = label(name, 'page-title')
        heading.append(title)
        heading.append(Gtk.Box(hexpand=True))
        content.append(heading)
        return scroll(clamp(content)), content, heading

    def library(self):
        page, content, heading = self.page('我的收藏')
        count = label(f'{len(TRACKS)} 首', 'dim-label')
        heading.insert_child_after(count, heading.get_first_child())
        actions = box(spacing=8)
        actions.append(button('播放全部', action=self.play_all, style='suggested-action'))
        if not self.live:
            actions.append(button('导入收藏', action=lambda: self.dialog('导入收藏', ['华为音乐'], '导入')))
        content.append(actions)
        self.favorite_area = content
        heading.get_first_child().get_next_sibling().set_text(f'{len(TRACKS)} 首')
        self.favorite_count = count
        self.add_tracks(content, range(len(TRACKS)))
        return page

    def add_tracks(self, parent, indexes, favorite=True, add_to_queue=False):
        listing = Gtk.ListBox(selection_mode=Gtk.SelectionMode.NONE, css_classes=['track-list'])
        listing.connect('row-activated', lambda _, r: self.play_from_list(r.track, add_to_queue))
        for n in indexes:
            title, artist, album, _ = TRACKS[n]
            row = Gtk.ListBoxRow()
            row.track = n
            line = margin(box(spacing=10), 8, 6)
            line.append(self.art(n, 40))
            names = box(True, 1)
            names.set_valign(Gtk.Align.CENTER)
            names.set_hexpand(True)
            names.append(label(title, 'row-title'))
            names.append(label(artist, 'dim-label'))
            line.append(names)
            if favorite and not self.live:
                like = button('收藏', 'starred-symbolic' if n in self.favorites else 'non-starred-symbolic', lambda x=n: self.like(x), 'flat')
                like.set_valign(Gtk.Align.CENTER)
                line.append(like)
                self.liked_buttons.append((n, like))
            entries = [('专辑', lambda x=album,n=n: self.detail(x, [n])), ('艺人', lambda x=artist,n=n: self.detail(x, [n])),
                       ('添加到歌单', lambda: self.dialog('添加到歌单', ['夜间列车'], '添加'))]
            if not self.live:
                more = menu('曲目操作', entries)
                more.set_valign(Gtk.Align.CENTER)
                line.append(more)
            row.set_child(line)
            self.track_rows.append(row)
            listing.append(row)
        parent.append(listing)
        return listing

    def playlist_rows(self):
        return [('夜间列车','本地 · 4 首'), ('海风','本地 · 3 首'), ('春日漫游','华为音乐 · 只读 · 3 首')]

    def open_playlist(self, n, title, source):
        self.detail(title, range(3), source)

    def playlists(self):
        page, content, _ = self.page('我的歌单')
        self.playlist_area = content
        actions = box(spacing=8)
        if not self.live:
            actions.append(button('新建歌单', action=lambda: self.dialog('新建歌单', [], '创建', field='歌单名称'), style='suggested-action'))
        if not self.live:
            actions.append(button('导入歌单', action=lambda: self.dialog('导入歌单', ['华为音乐'], '导入')))
        content.append(actions)
        grid = self.playlist_grid = Gtk.Grid(column_spacing=8, row_spacing=12, halign=Gtk.Align.START,
                                            css_classes=['playlist-grid'])
        self.playlist_cards = []
        self.playlist_columns = None
        for n, (name, meta) in enumerate(self.playlist_rows()):
            kind = 'imported' if n == 2 else 'local'
            row = button(name, action=lambda i=n, title=name, source=kind: self.open_playlist(i, title, source), style='flat')
            row.add_css_class('playlist-card')
            row.update_property([Gtk.AccessibleProperty.LABEL], [f'{name}，{meta}'])
            tile = box(True, 3)
            tile.set_size_request(140, -1)
            art = self.playlist_art(n)
            art.set_halign(Gtk.Align.START)
            art.set_margin_bottom(6)
            tile.append(art)
            tile.append(label(name, 'row-title'))
            metadata = label(meta, 'playlist-meta')
            metadata.add_css_class('dim-label')
            metadata.set_max_width_chars(12)
            tile.append(metadata)
            row.set_child(tile)
            self.playlist_cards.append(row)
        self.layout_playlists(3)
        content.append(grid)
        return page

    def playlist_art(self, n):
        return cover(n, 140, rounded=True)

    def layout_playlists(self, columns):
        if self.playlist_columns == columns:
            return
        for card in self.playlist_cards:
            if card.get_parent():
                self.playlist_grid.remove(card)
        for index, card in enumerate(self.playlist_cards):
            self.playlist_grid.attach(card, index % columns, index // columns, 1, 1)
        self.playlist_columns = columns

    def search(self):
        page, area, _ = self.page('搜索')
        line = box(spacing=8)
        entry = Gtk.SearchEntry(placeholder_text='歌曲或艺人', hexpand=True)
        line.append(entry)
        line.append(button('搜索', action=lambda: entry.emit('activate'), style='suggested-action'))
        area.append(line)
        results = box(True)
        results.set_vexpand(True)
        area.append(results)
        self.search_results = results
        def submit(*_):
            while results.get_first_child():
                results.remove(results.get_first_child())
            query = entry.get_text().strip()
            if self.live:
                self.submit_search(query)
                return
            query = query.casefold()
            if not query:
                return
            found = [i for i, t in enumerate(TRACKS) if query in (' '.join(t[:2])).casefold()]
            if found:
                self.add_tracks(results, found, add_to_queue=True)
            else:
                results.append(Adw.StatusPage(icon_name='system-search-symbolic', title='未找到结果', vexpand=True, valign=Gtk.Align.CENTER))
        entry.connect('activate', submit)
        entry.connect('search-changed', lambda *_: submit() if not entry.get_text() else None)
        self.search_entry = entry
        return page

    def detail(self, title, indexes, kind=None):
        self.return_page = self.view if self.view != 'detail' else self.return_page
        self.detail_title = title
        if old := self.screen.get_child_by_name('detail'):
            self.screen.remove(old)
        page, area, _ = self.page(title)
        actions = box(spacing=8)
        if indexes:
            actions.append(button('播放全部', action=lambda: self.play_from_list(next(iter(indexes))), style='suggested-action'))
        if kind:
            action = '更新本地副本' if kind == 'imported' else '删除歌单'
            actions.append(button(action, action=lambda: self.dialog(action, [title], '确认')))
        area.append(actions)
        self.add_tracks(area, indexes)
        self.screen.add_named(page, 'detail')
        self.open('detail')

    def settings(self):
        page, area, _ = self.page('设置')
        tabs = Adw.ViewStack()
        switch = Adw.ViewSwitcher(stack=tabs, policy=Adw.ViewSwitcherPolicy.NARROW, halign=Gtk.Align.START)
        area.append(switch)
        area.append(tabs)
        for key, name, glyph in [('sources','音源','network-server-symbolic'), ('play','播放','media-playback-start-symbolic'),
                                 ('appearance','外观','applications-graphics-symbolic'), ('advanced','高级','preferences-other-symbolic')]:
            slot = margin(box(True, 14), 0, 8)
            tabs.add_titled_with_icon(slot, key, name, glyph)
            group = Adw.PreferencesGroup()
            slot.append(group)
            def action(title, choices=()):
                row = Adw.ActionRow(title=title)
                group.add(row)
                if choices:
                    b = button(choices[0], action=lambda: self.dialog(title, list(choices[1:]), '确定'), style='flat')
                    b.set_valign(Gtk.Align.CENTER)
                    row.add_suffix(b)
                    row.set_activatable_widget(b)
                return row
            def combo(title, choices):
                group.add(Adw.ComboRow(title=title, model=Gtk.StringList.new(choices)))
            if key == 'sources':
                row = action('华为音乐', ('登录',))
                row.set_subtitle('未登录')
                row.add_prefix(icon('network-server-symbolic'))
                row.add_suffix(Gtk.Switch(active=True, valign=Gtk.Align.CENTER, tooltip_text='启用音源'))
                actions = box(spacing=8)
                actions.append(button('添加音源', action=lambda: self.dialog('添加音源', [], '添加', field='音源包 (.zip)'), style='suggested-action'))
                actions.append(menu('音源操作', [('从文件夹添加…', lambda: self.dialog('从文件夹添加', [], '添加', field='文件夹')), ('打开音源文件夹', None)]))
                slot.prepend(actions)
            elif key == 'play':
                combo('关闭窗口时', ['退出程序', '隐藏窗口'])
                combo('默认音质', ['标准', '无损'])
                combo('播放模式', ['顺序播放', '列表循环', '单曲循环', '随机播放'])
                group.add(Adw.SwitchRow(title='第三方歌词匹配'))
                for name in ['清除网络缓存', '清除本地导入', '导入本地歌词', '移除本曲导入']:
                    action(name, ('打开',))
            elif key == 'appearance':
                size = Adw.SpinRow.new_with_range(85, 120, 1)
                size.set_title('字体大小')
                size.set_value(100)
                group.add(size)
                action('系统字体', ('选择', 'Noto Sans CJK SC'))
                combo('主题', THEME_OPTIONS)
                action('恢复默认外观', ('恢复',))
            elif key == 'advanced':
                group.add(Adw.SwitchRow(title='启用模型辅助判断'))
                group.add(Adw.EntryRow(title='API 地址'))
                group.add(Adw.PasswordEntryRow(title='API Key'))
                group.add(Adw.EntryRow(title='模型 ID'))
                action('测试连接', ('测试',))
                action('保存配置', ('保存',))
        return page

    def dialog(self, title, choices, affirmative, field=None, cb=None):
        d = Adw.AlertDialog(heading=title)
        area = box(True, 8)
        if field:
            area.append(Gtk.Entry(placeholder_text=field))
        for choice in choices:
            area.append(label(choice))
        if choices or field:
            d.set_extra_child(area)
        d.add_response('cancel', '取消')
        d.add_response('confirm', affirmative)
        d.set_response_appearance('confirm', Adw.ResponseAppearance.DESTRUCTIVE if title in ('删除歌单','清空队列') else Adw.ResponseAppearance.SUGGESTED)
        if cb:
            d.connect('response::confirm', lambda *_: cb())
        d.present(self.win)

    def queue_view(self):
        panel = Adw.ToolbarView()
        head = Adw.HeaderBar(show_start_title_buttons=False, show_end_title_buttons=False)
        head.set_title_widget(Adw.WindowTitle(title='播放队列'))
        head.pack_start(button('清空队列', 'edit-clear-all-symbolic', lambda: self.dialog('清空队列', [], '清空', cb=self.clear_queue), 'flat'))
        head.pack_end(button('关闭队列', 'window-close-symbolic', self.toggle_queue, 'flat'))
        panel.add_top_bar(head)
        self.queue_list = Gtk.ListBox(selection_mode=Gtk.SelectionMode.NONE, css_classes=['track-list'])
        self.queue_list.connect('row-activated', lambda _, row: self.activate_queue_row(row))
        panel.set_content(scroll(margin(self.queue_list, 8, 6)))
        return panel

    def activate_queue_row(self, row):
        self.select(row.track)

    def refresh_queue(self):
        while self.queue_list.get_first_child():
            self.queue_list.remove(self.queue_list.get_first_child())
        for i, n in enumerate(self.queue):
            row = Gtk.ListBoxRow()
            row.track = n
            if n == self.current:
                row.add_css_class('current')
            line = margin(box(spacing=9), 8, 6)
            line.append(self.art(n, 35))
            names = box(True, 1)
            names.set_hexpand(True)
            names.append(label(TRACKS[n][0], 'row-title'))
            names.append(label(TRACKS[n][1], 'dim-label'))
            line.append(names)
            line.append(button('移出队列', 'list-remove-symbolic', lambda x=i: self.remove_queue(x), 'flat'))
            row.set_child(line)
            self.queue_list.append(row)

    def quality_entries(self):
        return [('标准', None), ('无损', None)]

    def compact_quality_entries(self):
        return [('音质', lambda: self.dialog('音质', ['标准','无损'], '确定'))]

    def player(self):
        outer = self.player_inner = margin(box(True, 6), 18, 10)
        surface = box(True)
        surface.add_css_class('player')
        surface.append(outer)
        identity = self.identity = box(spacing=10)
        identity.set_hexpand(True)
        identity.set_valign(Gtk.Align.CENTER)
        self.cover_btn = button('沉浸歌词', action=self.toggle_lyrics, style='flat')
        self.cover_btn.add_css_class('cover-button')
        self.cover_btn.set_tooltip_text('沉浸歌词')
        identity.append(self.cover_btn)
        names = box(True, 1)
        names.set_valign(Gtk.Align.CENTER)
        names.set_hexpand(True)
        self.player_title, self.player_artist = label('', 'row-title'), label('', 'dim-label')
        names.append(self.player_title)
        names.append(self.player_artist)
        identity.append(names)
        center = self.transport = box(spacing=6)
        center.set_halign(Gtk.Align.CENTER)
        center.set_valign(Gtk.Align.CENTER)
        center.append(button('上一首', 'media-skip-backward-symbolic', lambda: self.step(-1), 'flat'))
        self.play_button = button('暂停', 'media-playback-pause-symbolic', self.toggle_play, 'suggested-action')
        self.play_button.add_css_class('transport-primary')
        self.play_button.set_halign(Gtk.Align.CENTER)
        self.play_button.set_valign(Gtk.Align.CENTER)
        center.append(self.play_button)
        center.append(button('下一首', 'media-skip-forward-symbolic', lambda: self.step(1), 'flat'))
        options = self.player_options = box(spacing=4)
        options.set_valign(Gtk.Align.CENTER)
        self.tools = box(spacing=3)
        self.tools.set_valign(Gtk.Align.CENTER)
        self.tools.append(menu('播放模式', [(x, lambda y=x: self.set_mode(y)) for x in ('顺序播放','列表循环','单曲循环','随机播放')], 'media-playlist-repeat-symbolic'))
        self.quality_button = menu('音质', self.quality_entries(), 'audio-x-generic-symbolic')
        self.tools.append(self.quality_button)
        options.append(self.tools)
        self.more = menu('播放选项', [
            ('播放模式', lambda: self.dialog('播放模式', ['顺序播放','列表循环','单曲循环','随机播放'], '确定')),
            *self.compact_quality_entries()])
        options.append(self.more)
        self.volume_button = Gtk.VolumeButton()
        options.append(self.volume_button)
        self.queue_button = button('播放队列', 'view-list-symbolic', self.toggle_queue, 'flat')
        options.append(self.queue_button)
        self.time = label('1:42', 'duration')
        self.time.add_css_class('dim-label')
        self.total = label('4:26', 'duration')
        self.total.add_css_class('dim-label')
        self.seek = Gtk.Scale.new_with_range(Gtk.Orientation.HORIZONTAL, 0, 266, 1)
        self.seek.set_hexpand(True)
        self.seek.set_draw_value(False)
        self.seek.set_value(102)
        self.seek.update_property([Gtk.AccessibleProperty.LABEL], ['播放进度'])
        self.seek.connect('value-changed', lambda w: self.time.set_text(f'{int(w.get_value())//60}:{int(w.get_value())%60:02}'))
        self.progress = box(spacing=10)
        self.progress.append(self.time)
        self.progress.append(self.seek)
        self.progress.append(self.total)
        self.transport_column = box(True, 4)
        self.player_layout = Gtk.CenterBox(end_widget=options, hexpand=True)
        outer.append(self.player_layout)
        self.layout_player(False)
        return surface

    def layout_player(self, compact):
        if self.player_layout.get_center_widget():
            self.player_layout.set_center_widget(None)
        for part in (self.transport, self.progress):
            if parent := part.get_parent():
                parent.remove(part)
        if compact:
            self.progress.set_size_request(-1, -1)
            self.player_layout.set_center_widget(self.transport)
            self.player_inner.append(self.progress)
        else:
            self.progress.set_size_request(300, -1)
            self.transport_column.append(self.transport)
            self.transport_column.append(self.progress)
            self.player_layout.set_center_widget(self.transport_column)

    def layout_identity(self):
        compact = self.nav_split.get_collapsed()
        lyrics = self.view == 'lyrics'
        in_sidebar = not compact and not lyrics
        self.compact_brand.set_visible(not in_sidebar)
        start = (None if lyrics else self.identity) if compact else self.tools
        if self.player_layout.get_start_widget() is not start:
            self.player_layout.set_start_widget(None)
            if start is self.tools:
                self.player_options.remove(self.tools)
            elif start is self.identity and self.identity.get_parent() is self.sidebar_track:
                self.sidebar_track.remove(self.identity)
            self.player_layout.set_start_widget(start)
        if compact and not self.tools.get_parent():
            self.player_options.prepend(self.tools)
        if in_sidebar and not self.identity.get_parent():
            self.sidebar_track.append(self.identity)
        elif not in_sidebar and self.identity.get_parent() is self.sidebar_track:
            self.sidebar_track.remove(self.identity)

    def set_mode(self, mode):
        self.mode = mode

    def toggle_play(self):
        self.playing = not self.playing
        self.sync_play()

    def sync_play(self):
        self.play_button.set_icon_name('media-playback-pause-symbolic' if self.playing else 'media-playback-start-symbolic')
        text = '暂停' if self.playing else '播放'
        self.play_button.set_tooltip_text(text)
        self.play_button.update_property([Gtk.AccessibleProperty.LABEL], [text])

    def toggle_queue(self):
        self.queue_split.set_show_sidebar(not self.queue_split.get_show_sidebar())

    def queue_state(self):
        if hasattr(self, 'queue_button'):
            if self.queue_split.get_show_sidebar():
                self.queue_button.add_css_class('accent')
            else:
                self.queue_button.remove_css_class('accent')

    def select(self, n, keep_position=False):
        self.current = n
        self.playing = n is not None
        self.sync_play()
        self.cover_btn.set_child(cover(n, 40) if n is not None else icon('audio-x-generic-symbolic', 40))
        title, artist, _, length = TRACKS[n] if n is not None else ('未播放', '', '', '0:00')
        self.player_title.set_text(title)
        self.player_artist.set_text(artist)
        self.total.set_text(length)
        mins, secs = map(int, length.split(':'))
        self.seek.set_range(0, max(1, mins*60 + secs))
        if not keep_position:
            self.seek.set_value(0)
        for row in self.track_rows:
            if row.track == n:
                row.add_css_class('current')
            else:
                row.remove_css_class('current')
        self.refresh_queue()
        if self.view == 'lyrics':
            self.lyrics()

    def step(self, delta):
        if self.queue:
            index = self.queue.index(self.current) if self.current in self.queue else 0
            self.select(self.queue[(index + delta) % len(self.queue)])

    def play_from_list(self, n, append=False):
        if not append:
            self.queue = list(range(n, len(TRACKS)))
        elif n not in self.queue:
            self.queue.append(n)
        self.select(n)

    def play_all(self):
        self.queue = sorted(self.favorites)
        self.select(self.queue[0] if self.queue else None)

    def clear_queue(self):
        self.queue = []
        self.select(None)

    def remove_queue(self, index):
        n = self.queue.pop(index)
        if n == self.current:
            self.select(self.queue[min(index, len(self.queue)-1)] if self.queue else None)
        else:
            self.refresh_queue()

    def like(self, n):
        self.favorites.symmetric_difference_update({n})
        active = n in self.favorites
        for i, b in self.liked_buttons:
            if i == n:
                b.set_icon_name('starred-symbolic' if active else 'non-starred-symbolic')
                b.set_tooltip_text('取消收藏' if active else '收藏')

    def toggle_lyrics(self):
        if self.view == 'lyrics':
            self.open(self.lyrics_from)
        else:
            self.lyrics_from = self.view
            self.lyrics()

    def lyric_lines(self):
        return ('风翻过空白的信纸', '船灯从远处慢慢靠近', '等潮汐把你的名字带回来', '让沉默也有了回声', '我在岸边 等下一封信')

    def lyric_current(self):
        return 2

    def stage_art(self, n, size):
        return self.art(n, size)

    def append_stage_metadata(self, artwork):
        pass

    def lyrics(self):
        if old := self.screen.get_child_by_name('lyrics'):
            self.screen.remove(old)
        if self.current is None:
            self.stage_lines = []
            self.screen.add_named(Adw.StatusPage(title='暂无歌词', icon_name='view-subtitles-symbolic'), 'lyrics')
            self.open('lyrics')
            return
        outfit = Adw.BreakpointBin()
        spread = margin(box(spacing=54), 36, 24)
        spread.set_homogeneous(True)
        spread.set_valign(Gtk.Align.CENTER)
        artwork = box(True, 14)
        artwork.set_valign(Gtk.Align.CENTER)
        artwork.set_halign(Gtk.Align.CENTER)
        artwork_variants = Gtk.Stack(hhomogeneous=False, vhomogeneous=False)
        for name, size in (('compact', 136), ('regular', 296), ('large', 424)):
            slot = Gtk.Overlay(child=self.stage_art(self.current, size), overflow=Gtk.Overflow.HIDDEN)
            slot.add_css_class("album-art")
            face = StageCoverFace(self.stage_backdrop)
            slot.add_overlay(face)
            slot.set_measure_overlay(face, False)
            artwork_variants.add_named(slot, name)
        artwork_variants.set_visible_child_name('regular')
        artwork.append(artwork_variants)
        self.stage_cover = artwork_variants
        title = label(TRACKS[self.current][0], 'title-2')
        title.set_halign(Gtk.Align.CENTER)
        artwork.append(title)
        artist = label(TRACKS[self.current][1], 'dim-label')
        artist.set_halign(Gtk.Align.CENTER)
        artwork.append(artist)
        self.append_stage_metadata(artwork)
        spread.append(artwork)
        text = box(True, 20)
        text.set_valign(Gtk.Align.CENTER)
        self.stage_lines = []
        active = self.lyric_current()
        for i, line in enumerate(self.lyric_lines()):
            item = label(line, 'lyric')
            item.add_css_class('active' if i == active else 'dim-label')
            item.set_wrap(True)
            item.set_ellipsize(Pango.EllipsizeMode.NONE)
            text.append(item)
            self.stage_lines.append(item)
        self.stage_text = text
        if self.live:
            self.stage_scroll = scroll(text)
            spread.set_valign(Gtk.Align.FILL)
            spread.append(self.stage_scroll)
        else:
            spread.append(text)
        outfit.set_size_request(320, 420)
        outfit.set_vexpand(True)
        outfit.set_child(clamp(spread, 1700))
        desktop = Adw.Breakpoint.new(Adw.BreakpointCondition.parse('min-width: 1340px and min-height: 580px'))
        outfit.add_breakpoint(desktop)
        desktop.add_setter(artwork_variants, 'visible-child-name', 'large')
        compact = Adw.Breakpoint.new(Adw.BreakpointCondition.parse('max-width: 740px'))
        outfit.add_breakpoint(compact)
        compact.add_setter(spread, 'orientation', Gtk.Orientation.VERTICAL)
        compact.add_setter(spread, 'homogeneous', False)
        compact.add_setter(spread, 'spacing', 26)
        compact.add_setter(artwork_variants, 'visible-child-name', 'compact')
        compact.add_setter(text, 'spacing', 10)
        self.screen.add_named(outfit if self.live else scroll(outfit), 'lyrics')
        self.open('lyrics')
        if self.live:
            self.art_follow_lyric(active)


def smoke(ui):
    assert ui.screen.get_visible_child_name() == 'favorites'
    assert ui.nav_split.get_collapsed() == (ui.args.width <= 720), (ui.win.get_width(), ui.root.measure(Gtk.Orientation.HORIZONTAL, -1), ui.nav_split.get_collapsed())
    assert ui.queue_split.get_collapsed() == (ui.args.width <= 1110)
    # X11 and Wayland disagree on whether CSD extents count toward this width.
    assert abs(ui.win.get_width() - ui.args.width) <= 16, ui.win.get_width()
    viewport = ui.screen.get_visible_child().get_vadjustment()
    assert viewport.get_upper() > viewport.get_page_size()
    viewport.set_value(viewport.get_upper())
    assert viewport.get_value() == viewport.get_upper() - viewport.get_page_size()
    assert ui.play_button.get_width() == ui.play_button.get_height() >= 40, (ui.play_button.get_width(), ui.play_button.get_height())
    if not ui.nav_split.get_collapsed():
        assert ui.identity.get_parent() is ui.sidebar_track
        assert ui.player_layout.get_start_widget() is ui.tools
        assert ui.queue_button.get_parent() is ui.player_layout.get_end_widget()
        assert ui.sidebar.get_height() == ui.main_pane.get_height()
        brand_icon = ui.brand_icon
        nav_icon = ui.nav.get_first_child().get_child().get_first_child()
        def center_x(widget):
            ok, rect = widget.compute_bounds(ui.root)
            assert ok
            return rect.get_x() + rect.get_width() / 2
        assert abs(center_x(brand_icon) - center_x(nav_icon)) <= 1, (center_x(brand_icon), center_x(nav_icon))
    ui.queue_button.emit('clicked')
    assert ui.queue_split.get_show_sidebar()
    ui.step(1)
    assert ui.current == 1 and ui.player_title.get_text() == TRACKS[1][0]
    ui.open('search')
    ui.search_entry.set_text('潮汐')
    ui.search_entry.emit('activate')
    listing = ui.search_results.get_first_child()
    assert listing.get_first_child().track == 0 and listing.get_last_child().track == 0
    before = list(ui.queue)
    listing.emit('row-activated', listing.get_first_child())
    assert ui.queue == before and ui.current == 0
    assert ui.volume_button.get_parent() is ui.player_layout.get_end_widget()
    ui.volume_button.set_value(.6)
    ui.cover_btn.emit('clicked')
    assert ui.screen.get_visible_child_name() == 'lyrics'
    assert ui.volume_button.get_value() == .6
    assert ui.identity.get_parent() is None
    if not ui.nav_split.get_collapsed():
        assert ui.player_layout.get_start_widget() is ui.tools
    ui.toggle_lyrics()
    assert ui.screen.get_visible_child_name() == 'search'
    expected_parent = ui.player_layout if ui.nav_split.get_collapsed() else ui.sidebar_track
    assert ui.identity.get_parent() is expected_parent
    assert ui.primary_menu.get_menu_model().get_n_items() == 3
    ui.win.lookup_action('preferences').activate(None)
    assert ui.screen.get_visible_child_name() == 'settings'
    ui.win.lookup_action('about').activate(None)
    assert ui.about_dialog.get_application_name() == '听澜'
    ui.about_dialog.close()
    ui.open('playlists')


def smoke_playlist(ui):
    tile = ui.playlist_cards[0]
    art = tile.get_child().get_first_child()
    ok, outer = tile.compute_bounds(ui.root)
    assert ok
    assert outer.get_width() - art.get_width() == 16, (outer.get_width(), art.get_width())
    assert ui.playlist_columns == (2 if ui.nav_split.get_collapsed() else 3)
    assert isinstance(tile.get_child(), Gtk.Box)
    tile.emit('clicked')
    assert ui.screen.get_visible_child_name() == 'detail' and ui.detail_title == '夜间列车'
    print(f'PASS {ui.win.get_width()}×{ui.win.get_height()}: breakpoints, queue, next, search, lyrics, settings', flush=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--width', type=int, default=1140)
    parser.add_argument('--height', type=int, default=760)
    parser.add_argument('--page', choices=[*PAGES, 'queue', 'lyrics'], default='favorites')
    theme = parser.add_mutually_exclusive_group()
    theme.add_argument('--dark', action='store_true')
    theme.add_argument('--light', action='store_true')
    parser.add_argument('--queue', action='store_true')
    parser.add_argument('--smoke', action='store_true')
    parser.add_argument('--backend', type=Path, help='Go backend executable; uses the current native account')
    args = parser.parse_args()
    app = Adw.Application(flags=Gio.ApplicationFlags.NON_UNIQUE)
    def activate(application):
        if args.dark:
            Adw.StyleManager.get_default().set_color_scheme(Adw.ColorScheme.FORCE_DARK)
        elif args.light:
            Adw.StyleManager.get_default().set_color_scheme(Adw.ColorScheme.FORCE_LIGHT)
        provider = Gtk.CssProvider()
        shared_style = asset("gtk_collection.css")
        provider.load_from_data(CSS + shared_style.read_bytes())
        display = Gdk.Display.get_default()
        Gtk.StyleContext.add_provider_for_display(display, provider, Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
        Gtk.IconTheme.get_for_display(display).add_search_path(str(Path(__file__).with_name('icons')))
        initial = args.page
        if initial == 'lyrics':
            args.page = 'favorites'
        if args.backend:
            from live import Live
            ui = Live(application, args, Preview)
        else:
            ui = Preview(application, args)
        if initial == 'lyrics':
            ui.toggle_lyrics()
        ui.win.present()
        if args.smoke and args.backend:
            ui.run_live_check(application)
        elif args.smoke:
            def run(last=False):
                try:
                    if not last:
                        smoke(ui)
                        GLib.timeout_add(260, run, True)
                        return GLib.SOURCE_REMOVE
                    smoke_playlist(ui)
                except Exception:
                    import traceback
                    traceback.print_exc()
                    application.smoke_failed = True
                application.quit()
                return False
            GLib.timeout_add(450, run)
    app.connect('activate', activate)
    result = app.run([])
    return 1 if getattr(app, 'smoke_failed', False) else result


if __name__ == '__main__':
    raise SystemExit(main())
