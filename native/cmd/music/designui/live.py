"""Bind the original design widgets to the local Go model over private pipes."""
import json
import subprocess
import threading
from time import monotonic


def Live(app, args, preview):
    # Use the same module as the design entry point (__main__ when run directly).
    import sys
    d = sys.modules[preview.__module__]
    Gtk, Adw, GLib = d.Gtk, d.Adw, d.GLib

    from live_collections import collections_mixin
    from live_settings import settings_mixin
    from live_art import art_mixin

    class BoundDesign(art_mixin(d), settings_mixin(d), preview):
        live = True

        def __init__(self):
            self.state, self.indexes, self.keys, self.lists = {}, {}, {}, {}
            self.sent, self.received, self.syncing, self.stopped = 0, 0, False, False
            self.query, self.detail_name = '', ''
            d.TRACKS = []
            super().__init__(app, args)
            self.current = None
            self.queue = []
            self.playing = False
            self.win.set_title('听澜')
            self.art_install()
            self.seek.set_value(0)
            self.total.set_text('0:00')
            self.sync_play()
            self.player_title.set_text('未在播放')
            self.cover_btn.set_child(self.art(None, 40))
            self.backend = subprocess.Popen([str(args.backend.resolve()), '--design-stdio'],
                stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True, encoding='utf-8')
            self.win.connect('close-request', self.close_backend)
            self.volume_button.connect('value-changed', lambda _, v: None if self.syncing else self.send('volume', Value=v))
            self.seek_target = None
            self.seek_drag_origin = 0
            self.seek_position = 0
            self.seek_drag = Gtk.GestureDrag(button=1, propagation_phase=Gtk.PropagationPhase.CAPTURE)
            self.seek_drag.connect('drag-begin', self.seek_begin)
            self.seek_drag.connect('drag-update', self.seek_preview)
            self.seek_drag.connect('drag-end', self.seek_end)
            self.seek_drag.connect('cancel', self.seek_cancel)
            self.seek.add_controller(self.seek_drag)
            self.seek.connect('change-value', self.seek_changed)
            threading.Thread(target=self.read_backend, daemon=True).start()
            self.next_normal_poll = monotonic() + .45
            GLib.timeout_add(100, self.poll)
            self.send('state')

        def run_live_check(self, application):
            import time
            deadline = time.monotonic() + 26
            phase = 0

            def tick():
                nonlocal phase
                state = self.state
                if time.monotonic() > deadline or self.stopped:
                    print(f'LIVE CHECK FAILED at phase {phase}', flush=True)
                    application.smoke_failed = True
                    self.win.close()
                    application.quit()
                    return False
                if phase == 0 and state.get('SignedIn') and 'huawei' in (state.get('Sources') or []):
                    print(f"LIVE: session restored; favorites={len(state['Favorites'])}", flush=True)
                    self.open('search')
                    self.search_entry.set_text('Adele')
                    self.search_entry.emit('activate')
                    phase = 1
                elif phase == 1 and self.query and state.get('Results') and not state.get('Searching'):
                    row = self.lists['Results'].get_first_child()
                    if row is not None:
                        print(f"LIVE: GTK search rendered {len(state['Results'])} results", flush=True)
                        self.lists['Results'].emit('row-activated', row)
                        phase = 2
                elif phase == 2 and state.get('Playing') and state.get('Position',0) >= 1:
                    inputs = json.loads(subprocess.check_output(['pactl','-f','json','list','sink-inputs'], text=True, timeout=2))
                    active = any(not entry['corked'] and not entry['mute'] and
                                 entry['properties'].get('application.process.id') == str(self.backend.pid) for entry in inputs)
                    if active:
                        print('LIVE: selected search result played; PipeWire stream active', flush=True)
                        self.win.close()
                        application.quit()
                        return False
                return True

            GLib.timeout_add(200, tick)

        def close_backend(self, *_):
            if (not self.stopped and not self.args.smoke and not getattr(self, "force_quit", False)
                    and self.state.get("Settings", {}).get("CloseToTray")):
                self.win.hide()
                return True
            if not self.stopped:
                self.stopped = True
                try:
                    self.backend.stdin.close()
                except OSError:
                    pass
            return False

        def quit_window(self):
            self.force_quit = True
            self.win.close()

        def read_backend(self):
            try:
                for line in self.backend.stdout:
                    GLib.idle_add(self.apply, json.loads(line))
            finally:
                self.backend.stdout.close()
                self.backend.wait()
                GLib.idle_add(self.backend_closed)

        def backend_closed(self):
            if not self.stopped:
                self.win.set_title('听澜 · Go 后端已断开')
                self.play_button.set_sensitive(False)
            self.stopped = True
            return False

        def send(self, op, **data):
            if not hasattr(self, 'backend') or self.stopped:
                return
            try:
                self.backend.stdin.write(json.dumps(dict(Op=op, **data), ensure_ascii=False) + '\n')
                self.backend.stdin.flush()
                self.sent += 1
            except OSError:
                self.backend_closed()

        def poll(self):
            if self.stopped:
                return False
            now = monotonic()
            if self.sent == self.received and (self._art_request is not None or now >= self.next_normal_poll):
                self.next_normal_poll = now + .45
                self.send('state')
            return True

        def seek_current_target(self):
            rows = self.state.get('Queue') or []
            index = self.state.get('Current', -1)
            return (index, rows[index]['ID']) if 0 <= index < len(rows) else None

        def sync_seek(self, duration, position, pending=None):
            self.seek_position = position
            if self.seek_target is not None and self.seek_target != self.seek_current_target():
                self.seek_target = None
            self.seek.set_range(0, max(1, duration))
            if self.seek_target is None:
                self.seek.set_value(position if pending is None else pending)

        def seek_begin(self, gesture, x, _):
            # Own the pointer sequence before the scale's native gesture.
            gesture.set_state(Gtk.EventSequenceState.CLAIMED)
            self.seek.grab_focus()
            self.seek_target = self.seek_current_target()
            self.seek_drag_origin = x
            self.seek_preview(gesture, 0, 0)

        def seek_preview(self, _, offset, __):
            if self.seek_target is None or self.seek_target != self.seek_current_target():
                return
            rect = self.seek.get_range_rect()
            left, right = self.seek.get_slider_range()
            inset = max(0, (right - left) / 2)
            fraction = (self.seek_drag_origin + offset - rect.x - inset) / max(1, rect.width - 2 * inset)
            if self.seek.get_inverted() != (self.seek.get_flippable() and self.seek.get_direction() == Gtk.TextDirection.RTL):
                fraction = 1 - fraction
            adj = self.seek.get_adjustment()
            self.seek.set_value(adj.get_lower() + max(0, min(1, fraction)) * (adj.get_upper() - adj.get_lower()))

        def seek_end(self, gesture, offset, y):
            if self.seek_target is None:
                return
            if self.seek_target != self.seek_current_target():
                self.seek_cancel(gesture, None)
                return
            self.seek_preview(gesture, offset, y)
            self.seek_target = None
            self.send('seek', Value=self.seek.get_value())

        def seek_cancel(self, _, __):
            if self.seek_target is not None:
                self.seek_target = None
                self.seek.set_value(self.seek_position)

        def seek_changed(self, _, __, value):
            # Non-drag changes (keyboard, scroll) still seek immediately.
            if not self.syncing and self.seek_target is None:
                self.send('seek', Value=value)
            return True

        def art(self, n, size, rounded=False):
            return self.art_build(n, size, rounded)

        def stage_background_texture(self):
            ref = self._art_ref(("Stage", self.current), self.state)
            if ref is None:
                return None
            high, _ = self._art_textures.get(self._art_key(ref), (None, 0))
            thumb, _ = self._art_textures.get((False, ref[2]), (None, 0))
            if thumb is not None and (high is None or thumb.get_width() > high.get_width()):
                return thumb
            return high

        def stage_art(self, n, size):
            return self.art_build(("Stage", n), size)

        def playlist_art(self, n):
            return self.art_playlist(n)

        def playlist_rows(self):
            return [(p['Name'], p['Subtitle']) for p in self.state.get('Playlists') or ()]

        def open_playlist(self, n, title, source):
            self.detail_name = title
            self.send('playlist', ID=self.state['Playlists'][n]['ID'])
            self.waiting_detail = True

        def settings(self):
            return self.settings_build()

        def submit_search(self, query):
            self.query = query
            self.search_dirty = True
            self.send('search', Text=query)

        def select(self, n, keep_position=False):
            if not hasattr(self, 'backend') or n is None:
                return
            if n in self.queue:
                self.play_action('queue-select', 'Queue', self.queue.index(n))

        def activate_queue_row(self, row):
            self.play_action('queue-select', 'Queue', row.get_index())

        def step(self, delta): self.send('step', Index=delta)
        def toggle_play(self): self.send('toggle')
        def clear_queue(self): self.send('queue-clear')
        def remove_queue(self, index): self.play_action('queue-remove', 'Queue', index)
        def play_all(self): self.play_action('play-favorites', 'Favorites', 0)
        def set_mode(self, mode): self.send('mode', Index=('顺序播放','列表循环','单曲循环','随机播放').index(mode))
        def open(self, key):
            previous = getattr(self, 'view', None)
            result = super().open(key)
            if key != 'lyrics' or previous != 'lyrics':
                self._lyrics_requested = None
            self.request_visible_lyrics()
            return result

        def request_visible_lyrics(self):
            if self.view != 'lyrics' or not hasattr(self, 'backend'):
                return
            token = (self.state.get('Art') or {}).get('LyricToken')
            if (token and self.state.get('LyricPhase') in ('cache', 'idle')
                    and token != getattr(self, '_lyrics_requested', None)):
                self._lyrics_requested = token
                self.send('lyrics-open', ID=token)

        def quality_entries(self):
            return [("标准", lambda: self.select_quality("standard")),
                    ("无损", lambda: self.select_quality("lossless"))]

        def compact_quality_entries(self):
            return [("音质 · " + label, action) for label, action in self.quality_entries()]

        def select_quality(self, tier):
            queue = self.state.get("Queue") or ()
            index = self.state.get("Current", -1)
            if 0 <= index < len(queue):
                self.send("quality-switch", ID=queue[index]["ID"], Text=tier)

        def append_stage_metadata(self, artwork):
            album = d.TRACKS[self.current][2]
            if album:
                album_label = d.label(album, "dim-label")
                album_label.set_halign(Gtk.Align.CENTER)
                artwork.append(album_label)
            # Measure the fixed-size slot, not the picture: Gtk.Picture's
            # aspect-ratio measurement can grow to the width of the artwork.
            badge = Gtk.Overlay()
            slot = Gtk.Box()
            slot.set_size_request(44, 18)
            badge.set_child(slot)
            picture = Gtk.Picture.new_for_filename(str(d.asset("icons/flac-badge.svg")))
            picture.set_content_fit(Gtk.ContentFit.CONTAIN)
            picture.update_property([Gtk.AccessibleProperty.LABEL], ["FLAC"])
            revealer = Gtk.Revealer(child=picture, transition_type=Gtk.RevealerTransitionType.CROSSFADE,
                                    transition_duration=180, reveal_child=self.state.get("QualityActual") == "lossless")
            badge.add_overlay(revealer)
            badge.set_measure_overlay(revealer, False)
            badge.set_halign(Gtk.Align.CENTER)
            badge.set_valign(Gtk.Align.CENTER)
            artwork.append(badge)
            self.stage_flac_badge = badge
            self.stage_flac_revealer = revealer

        def update_quality(self, state, old):
            if revealer := getattr(self, "stage_flac_revealer", None):
                revealer.set_reveal_child(state.get("QualityActual") == "lossless")
            labels = {"standard": "标准（MP3）", "lossless": "无损（FLAC）"}
            actual = labels.get(state.get("QualityActual"), "未确认")
            text = f"当前音质：{actual}"
            self.quality_button.set_tooltip_text(text)
            self.more.set_tooltip_text("播放选项 · " + text)
            self.quality_button.set_sensitive(state.get("Current", -1) >= 0)
            if state.get("QualityNoticeSerial") != old.get("QualityNoticeSerial") and state.get("QualityNotice"):
                overlay = getattr(self, "_collection_toast_overlay", None)
                if overlay is None:
                    overlay = Adw.ToastOverlay()
                    self.win.set_content(None)
                    overlay.set_child(self.root)
                    self.win.set_content(overlay)
                    self._collection_toast_overlay = overlay
                overlay.add_toast(Adw.Toast(title=state["QualityNotice"], timeout=3))

        def lyric_lines(self): return self.art_lyric_lines()
        def lyric_current(self): return self.art_lyric_current()

        def play_action(self, action, field, index):
            rows = self.state.get(field) or ()
            if 0 <= index < len(rows):
                self.send(action, Index=index, ID=rows[index]['ID'])

        def play_from_list(self, n, append=False):
            fields = {'favorites': ('Favorites','play-favorites'), 'search': ('Results','play-result'),
                      'history': ('History','play-history'), 'detail': ('Detail','play-detail')}
            if self.view in fields:
                field, action = fields[self.view]
                indexes = self.indexes.get(field, [])
                if n in indexes:
                    self.play_action(action, field, indexes.index(n))

        def indexes_for(self, rows):
            indexes = []
            for row in rows:
                key = row['ID']
                data = (row['Title'], row['Artist'], row['Album'], row['Duration'] or '0:00')
                if key not in self.keys:
                    self.keys[key] = len(d.TRACKS)
                    d.TRACKS.append(data)
                idx = self.keys[key]
                d.TRACKS[idx] = data
                indexes.append(idx)
            return indexes

        def render_list(self, field, area):
            if old := self.lists.get(field):
                if old.get_parent() == area:
                    area.remove(old)
            self.lists[field] = self.add_tracks(area, self.indexes[field], favorite=False, add_to_queue=field=='Results')
            attached = {row for row in self.track_rows if row.get_parent() is not None}
            self.track_rows = [row for row in self.track_rows if row in attached and row.get_parent().get_parent() is not None]

        def apply(self, state):
            self.received += 1
            if self.stopped:
                return False
            if state.get("Quit"):
                self.quit_window()
                return False
            if state.get("Raise"):
                self.win.present()
            old, self.state = self.state, state
            for field in ('Favorites','Results','History','Queue','Detail'):
                if state[field] != old.get(field):
                    self.indexes[field] = self.indexes_for(state[field] or [])
            if state['Favorites'] != old.get('Favorites'):
                # Remove the initially empty preview list before adding real rows.
                if 'Favorites' not in self.lists:
                    self.lists['Favorites'] = self.favorite_area.get_last_child()
                self.render_list('Favorites', self.favorite_area)
                self.favorite_count.set_text(f"{len(state['Favorites'])} 首")
            if state['History'] != old.get('History'):
                if 'History' not in self.lists:
                    self.lists['History'] = self.history_area.get_last_child()
                self.render_list('History', self.history_area)
            if (state['Results'], state['Searching'], state['More'], state['SearchError']) != (old.get('Results'), old.get('Searching'), old.get('More'), old.get('SearchError')) or getattr(self,'search_dirty',False):
                self.search_dirty = False
                while child := self.search_results.get_first_child():
                    self.search_results.remove(child)
                if state['Results']:
                    self.render_list('Results', self.search_results)
                if state['More']:
                    self.search_results.append(d.button('加载更多', action=lambda: self.send('more')))
                if self.query and not state['Results']:
                    text = '正在搜索…' if state['Searching'] else ('搜索失败' if state['SearchError'] else '未找到结果')
                    self.search_results.append(Adw.StatusPage(icon_name='system-search-symbolic', title=text, vexpand=True, valign=Gtk.Align.CENTER))
            if state['Playlists'] != old.get('Playlists'):
                self.screen.remove(self.screen.get_child_by_name('playlists'))
                self.screen.add_named(self.playlists(), 'playlists')
                self.layout_for_width()
                if self.view == 'playlists': self.open('playlists')
            if getattr(self,'waiting_detail',False):
                self.waiting_detail = False
                self.detail(self.detail_name, self.indexes['Detail'])
            self.settings_apply(state)
            self.update_quality(state, old)
            last = self.current
            self.queue = self.indexes.get('Queue', [])
            self.current = self.queue[state['Current']] if 0 <= state['Current'] < len(self.queue) else None
            title, artist, _, duration = d.TRACKS[self.current] if self.current is not None else ('未在播放','','','0:00')
            self.player_title.set_text(title)
            self.player_artist.set_text(artist)
            self.playing = state['Playing']
            self.sync_play()
            length = state['Duration'] or sum(int(v)*60**i for i,v in enumerate(reversed(duration.split(':'))) if v.isdigit())
            self.syncing = True
            self.volume_button.set_value(state['Volume'])
            self.sync_seek(length, state['Position'], state.get('SeekTarget') if state.get('SeekPending') else None)
            self.total.set_text(f'{int(length)//60}:{int(length)%60:02}')
            self.syncing = False
            if last != self.current or state['Queue'] != old.get('Queue'):
                self.refresh_queue()
            for row in self.track_rows:
                (row.add_css_class if row.track==self.current else row.remove_css_class)('current')
            wire = state.get("Art") or {}
            epoch = (wire.get("Epoch"), wire.get("CacheRevision", 0))
            if epoch != self._art_epoch:
                self.set_stage_texture(None, animate=False)
            self.art_apply(state)
            self.refresh_stage_backdrop()
            if self.view == 'lyrics':
                self.request_visible_lyrics()
                if (last != self.current or state.get('Lyrics') != old.get('Lyrics')
                        or state.get('LyricPhase') != old.get('LyricPhase')):
                    self.lyrics()
                elif hasattr(self,'stage_text'):
                    child = self.stage_text.get_first_child()
                    i = 0
                    active = self.lyric_current()
                    while child:
                        if i==active:
                            child.add_css_class('active'); child.remove_css_class('dim-label')
                        else:
                            child.remove_css_class('active'); child.add_css_class('dim-label')
                        child=child.get_next_sibling(); i += 1
                    self.art_follow_lyric(active)
            return False

    class IntegratedDesign(collections_mixin(d), BoundDesign):
        pass

    return IntegratedDesign()
