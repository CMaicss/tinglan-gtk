"""Bounded cover delivery and native lyric actions for the live design.

Install art_mixin(d) ahead of the preview base, use art_build in art(),
art_playlist in playlist_art(), and art_apply after applying each snapshot.
Call art_install after creating the window, and delegate the lyric line/current
hooks to art_lyric_lines and art_lyric_current.
"""
from base64 import b64decode
from bisect import bisect_right
from collections import OrderedDict
from time import monotonic
from weakref import WeakSet


_SCAN = 96
_CACHE = 96
_RETRY = 15
_STAGE_RETRY = 60
_STAGE_ENOUGH = 640
_MAX_PNG_WIRE = 350_000  # bounded 256 KiB cover payload, base64 encoded
_TRACK_FIELDS = ('Queue', 'Favorites', 'Results', 'History', 'Detail')


def art_mixin(d):
    from gi.repository import Gdk, GLib
    Gtk = d.Gtk

    class LiveArt:
        def _art_init(self):
            if hasattr(self, '_art_mapped'):
                return
            self._art_mapped = OrderedDict()
            self._art_textures = OrderedDict()
            self._art_shown = {}
            self._art_epoch = None
            self._art_request = None
            self._art_scrolls = WeakSet()
            self._art_refresh_queued = False
            self._art_player = None
            self._art_refs = {}
            self._art_indexes = {}
            self._art_state = {}
            self._art_lyrics = None
            self._art_labels = ['暂无歌词']
            self._art_cues = []
            self._art_picker = None

        def art_build(self, n, size, rounded=False):
            self._art_init()
            root = Gtk.Overlay(overflow=Gtk.Overflow.HIDDEN, halign=Gtk.Align.CENTER, valign=Gtk.Align.CENTER)
            root.set_size_request(size, size)
            root.add_css_class('playlist-art' if rounded or size >= 80 else 'art')
            icon = d.icon('audio-x-generic-symbolic', min(size, 48))
            root.set_child(icon)
            picture = Gtk.Picture(can_shrink=True, content_fit=Gtk.ContentFit.COVER)
            picture.set_can_target(False)
            picture.set_visible(False)
            root.add_overlay(picture)
            root.set_measure_overlay(picture, False)
            if n is not None:
                root.connect('map', lambda w: self._art_map(w, n, picture, icon))
                root.connect('unmap', self._art_unmap)
            return root

        def art_playlist(self, n):
            return self.art_build(('Playlists', n), 140, True)

        def _art_map(self, widget, n, picture, icon):
            self._art_mapped[widget] = (n, picture, icon)
            parent = widget.get_parent()
            while parent is not None and parent is not self.win:
                if isinstance(parent, Gtk.Viewport):
                    for adjustment in (parent.get_hadjustment(), parent.get_vadjustment()):
                        if adjustment not in self._art_scrolls:
                            self._art_scrolls.add(adjustment)
                            adjustment.connect('value-changed', self._art_schedule_refresh)
                parent = parent.get_parent()
            self._art_schedule_refresh()

        def _art_schedule_refresh(self, *_):
            if self._art_refresh_queued:
                return
            self._art_refresh_queued = True
            def refresh():
                self._art_refresh_queued = False
                if not getattr(self, 'stopped', False) and getattr(self, 'state', None):
                    self._art_refresh(self.state, cache_only=True)
                return False
            GLib.idle_add(refresh)

        def _art_unmap(self, widget):
            _, picture, icon = self._art_mapped.pop(widget, (None, None, None))
            self._art_shown.pop(widget, None)
            if picture is not None:
                picture.set_paintable(None)
                picture.set_visible(False)
                icon.set_visible(True)

        def _art_paint(self, widget, texture, asset=None):
            old = self._art_shown.get(widget)
            if (old is None and texture is None) or (old is not None and old[1] is texture):
                return
            _, picture, icon = self._art_mapped[widget]
            picture.set_paintable(texture)
            picture.set_visible(texture is not None)
            icon.set_visible(texture is None)
            if texture is None:
                self._art_shown.pop(widget, None)
            else:
                self._art_shown[widget] = (asset, texture)

        def _art_visible(self, widget):
            # ListBox rows can be mapped even several screens outside the
            # viewport. Intersect their allocation with every clipping view.
            parent = widget.get_parent()
            while parent is not None:
                if isinstance(parent, Gtk.Viewport):
                    # Compare content coordinates with the viewport's scroll
                    # adjustments, rather than a possibly stale frame transform.
                    ok, rect = widget.compute_bounds(parent.get_child())
                    h, v = parent.get_hadjustment(), parent.get_vadjustment()
                    if (not ok or rect.get_x() >= h.get_value() + h.get_page_size()
                            or rect.get_y() >= v.get_value() + v.get_page_size()
                            or rect.get_x() + rect.get_width() <= h.get_value()
                            or rect.get_y() + rect.get_height() <= v.get_value()):
                        return False
                elif parent is self.win and not widget.get_mapped():
                    return False
                if parent is self.win:
                    return True
                parent = parent.get_parent()
            return False

        def _art_ref(self, n, state):
            if isinstance(n, tuple):
                field, index = n
                if field == 'Stage':
                    if index != self.current:
                        return None
                    index = state.get('Current', -1)
                rows = state.get('Queue' if field == 'Stage' else field) or ()
                return (field, index, rows[index]['ID']) if 0 <= index < len(rows) else None
            # Prefer the exact current queue entry for player/stage covers.
            i = state.get('Current', -1)
            if n == self.current and 0 <= i < len(state.get('Queue') or ()):
                return 'Queue', i, state['Queue'][i]['ID']
            for field in _TRACK_FIELDS:
                if (ref := self._art_refs.get(field, {}).get(n)) is not None:
                    return ref
            return None

        @staticmethod
        def _art_key(ref):
            return ('Stage' if ref[0] == 'Stage' else ref[0] == 'Playlists', ref[2])

        def art_apply(self, state):
            self._art_init()
            wire = state.get('Art') or {}
            epoch = (wire.get('Epoch'), wire.get('CacheRevision', 0))
            if epoch != self._art_epoch:
                self._art_epoch = epoch
                self._art_textures.clear()
                for widget in tuple(self._art_shown):
                    self._art_paint(widget, None)
                self._art_request = None
            self._art_state = wire
            player = self.cover_btn.get_child()
            if self.current != self._art_player or not isinstance(player, Gtk.Overlay):
                self._art_player = self.current
                player = self.art_build(self.current, 40)
                self.cover_btn.set_child(player)
            for field in _TRACK_FIELDS:
                indexes = self.indexes.get(field)
                if indexes is not self._art_indexes.get(field):
                    self._art_indexes[field] = indexes
                    self._art_refs[field] = {n: (field, i, row['ID']) for i, (n, row) in
                                             enumerate(zip(indexes or (), state.get(field) or ()))}
            now = monotonic()
            if (reply := wire.get('Reply')) is not None:
                ref = (reply.get('Field'), reply.get('Index'), reply.get('ID'))
                if self._art_request == ref:
                    self._art_request = None
                    rows = state.get('Queue' if ref[0] == 'Stage' else ref[0]) or ()
                    if (0 <= ref[1] < len(rows) and rows[ref[1]]['ID'] == ref[2]
                            and (ref[0] != 'Stage' or ref[1] == state.get('Current'))):
                        key = self._art_key(ref)
                        texture = self._art_texture(reply.get('PNG'))
                        if ref[0] == 'Stage':
                            previous, _ = self._art_textures.get(key, (None, 0))
                            if previous is not None and (texture is None or previous.get_width() > texture.get_width()):
                                texture = previous
                        self._art_textures[key] = (texture, now)
                        self._art_textures.move_to_end(key)
                        while len(self._art_textures) > _CACHE:
                            old, _ = self._art_textures.popitem(last=False)
                            for widget, (asset, _) in tuple(self._art_shown.items()):
                                if asset == old:
                                    self._art_paint(widget, None)
            if not wire.get('Pending'):
                self._art_request = None
            self._art_refresh(state)
            self._art_update_lyric_controls()

        def _art_refresh(self, state, cache_only=False):
            wire = self._art_state
            player = self.cover_btn.get_child()
            now = monotonic()
            # Prioritize the player, then inspect a bounded round-robin batch
            # of mapped widgets. Invisible or clipped art does not request I/O.
            batch = []
            for _ in range(min(_SCAN, len(self._art_mapped))):
                widget = next(iter(self._art_mapped))
                self._art_mapped.move_to_end(widget)
                if widget is not player:
                    batch.append(widget)
            if player in self._art_mapped:
                batch.insert(0, player)
            batch.sort(key=lambda widget: not (isinstance(self._art_mapped[widget][0], tuple)
                                               and self._art_mapped[widget][0][0] == 'Stage'))
            for widget in batch:
                n, _, _ = self._art_mapped[widget]
                ref = self._art_ref(n, state) if self._art_visible(widget) else None
                if ref is None:
                    self._art_paint(widget, None)
                    continue
                key = self._art_key(ref)
                cached = self._art_textures.get(key)
                if ref[0] == 'Stage':
                    thumb_key = (False, ref[2])
                    thumb, _ = self._art_textures.get(thumb_key, (None, 0))
                    texture = cached[0] if cached is not None else None
                    if thumb is not None and (texture is None or thumb.get_width() > texture.get_width()):
                        self._art_paint(widget, thumb, thumb_key)
                    else:
                        self._art_paint(widget, texture, key)
                if cached is not None:
                    texture, seen = cached
                    if ref[0] != 'Stage':
                        self._art_paint(widget, texture, key)
                    if ref[0] == 'Stage':
                        settled = (texture is not None and texture.get_width() >= _STAGE_ENOUGH) or now - seen < _STAGE_RETRY
                    else:
                        settled = texture is not None or now - seen < _RETRY
                    if settled:
                        self._art_textures.move_to_end(key)
                        continue
                elif ref[0] != 'Stage' and widget in self._art_shown:
                    self._art_paint(widget, None)
                if (not cache_only and self._art_request is None and not wire.get('Pending')
                        and not wire.get('Clearing') and self.sent == self.received and wire.get('Epoch') is not None):
                    self._art_request = ref
                    self.send('art', Text=ref[0], Index=ref[1], ID=ref[2])

        @staticmethod
        def _art_texture(data):
            if not isinstance(data, str) or not 1 <= len(data) <= _MAX_PNG_WIRE:
                return None
            try:
                return Gdk.Texture.new_from_bytes(GLib.Bytes.new(b64decode(data, validate=True)))
            except (ValueError, GLib.Error):
                return None

        def art_install(self):
            self._art_init()
            old = self.lyric_options
            self.header.remove(old)
            self.lyric_options = d.menu('歌词选项', [
                ('导入歌词', self._art_open_lyrics),
                ('歌词提前半秒', lambda: self._art_offset(1)),
                ('歌词延后半秒', lambda: self._art_offset(-1)),
            ])
            self.header.pack_end(self.lyric_options)
            self.lyric_options.set_visible(self.view == 'lyrics')
            self._art_update_lyric_controls()
            self.win.connect('close-request', self._art_close_picker)

        def _art_update_lyric_controls(self):
            if hasattr(self, 'lyric_options'):
                wire = self._art_state
                self.lyric_options.set_sensitive(bool(wire.get('LyricToken')))
                feedback = wire.get('LyricFeedback') or '歌词选项'
                self.lyric_options.set_tooltip_text(feedback)

        def _art_offset(self, direction):
            if token := self._art_state.get('LyricToken'):
                self.send('lyric-offset', ID=token, Index=direction)

        def _art_close_picker(self, *_):
            if self._art_picker is not None:
                self._art_picker.destroy()
                self._art_picker = None
            return False

        def _art_open_lyrics(self):
            token = self._art_state.get('LyricToken')
            if not token or self._art_picker is not None:
                return
            chooser = Gtk.FileChooserNative.new('导入歌词', self.win, Gtk.FileChooserAction.OPEN, '导入', '取消')
            lyric_filter = Gtk.FileFilter(name='歌词文件')
            for suffix in ('lrc', 'ttml', 'xml', 'txt'):
                lyric_filter.add_suffix(suffix)
            chooser.add_filter(lyric_filter)
            all_files = Gtk.FileFilter(name='所有文件')
            all_files.add_pattern('*')
            chooser.add_filter(all_files)
            self._art_picker = chooser

            def response(dialog, result):
                try:
                    if result == Gtk.ResponseType.ACCEPT and token == self._art_state.get('LyricToken'):
                        selected = dialog.get_file()
                        if selected is not None and (path := selected.get_path()):
                            self.send('lyric-import', ID=token, Text=path)
                finally:
                    self._art_close_picker()

            chooser.connect('response', response)
            chooser.show()

        def _art_timeline(self):
            self._art_init()
            lines = self.state.get('Lyrics') or ()
            phase = self.state.get('LyricPhase')
            old, self._art_lyrics = self._art_lyrics, (phase, lines)
            if self._art_lyrics == old:
                return
            labels, cues = [], []
            for line in lines:
                text = line.get('text', '').strip()
                row = -1
                if text:
                    row = len(labels)
                    labels.append('\n'.join(filter(None, (text, line.get('translation'), line.get('romanization')))))
                if line.get('time', -1) >= 0:
                    cues.append((line['time'], row))
            empty = '正在加载歌词…' if phase in ('cache', 'idle', 'online') else '暂无歌词'
            self._art_labels = labels or [empty]
            self._art_cues = sorted(cues, key=lambda cue: cue[0])

        def art_follow_lyric(self, active):
            if active < 0 or active >= len(self.stage_lines):
                self._art_followed = None
                return
            if getattr(self, '_art_followed', None) == (self.stage_scroll, active):
                return
            panel, row = self.stage_scroll, self.stage_lines[active]
            self._art_followed = (panel, active)

            def center():
                if self._art_followed != (panel, active):
                    return False
                if not panel.get_mapped():
                    self._art_followed = None
                    return False
                adj = panel.get_vadjustment()
                ok, bounds = row.compute_bounds(self.stage_text)
                if not ok or not adj.get_page_size():
                    self._art_followed = None
                    return False
                y = bounds.get_y() + bounds.get_height() / 2 - adj.get_page_size() / 2
                y = max(adj.get_lower(), min(y, adj.get_upper() - adj.get_page_size()))
                if old := getattr(self, '_art_scroll_animation', None):
                    old.pause()
                from gi.repository import Adw
                target = Adw.CallbackAnimationTarget.new(adj.set_value)
                if not panel.get_settings().get_property("gtk-enable-animations"):
                    adj.set_value(y)
                    return False
                self._art_scroll_animation = Adw.TimedAnimation.new(panel, adj.get_value(), y, 220, target)
                self._art_scroll_animation.play()
                return False

            GLib.idle_add(center)

        def art_lyric_lines(self):
            self._art_timeline()
            return self._art_labels

        def art_lyric_current(self):
            self._art_timeline()
            offset = (self.state.get('Art') or {}).get('LyricOffset', 0)
            position = self.state.get('Position', 0) + offset
            end = bisect_right(self._art_cues, position, key=lambda cue: cue[0])
            return self._art_cues[end-1][1] if end else -1

    return LiveArt
