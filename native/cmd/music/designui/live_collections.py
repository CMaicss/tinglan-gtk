"""Collection bindings for the live design; the standalone preview stays synthetic.

Place the mixin before BoundDesign so it can intercept apply/open_playlist.
"""


def collections_mixin(d):
    class LiveCollections:
        def collection_state(self):
            return self.state.get('Collections') or {}

        def library(self):
            page = super().library()
            self.collection_favorite_import = d.button('导入收藏', action=lambda: self.collection_import(False))
            self.favorite_area.get_first_child().get_next_sibling().append(self.collection_favorite_import)
            self.collection_favorite_import.set_sensitive(False)
            return page

        def playlists(self):
            page = super().playlists()
            actions = self.playlist_area.get_first_child().get_next_sibling()
            self.collection_create_button = d.button('新建歌单', action=self.collection_create, style='suggested-action')
            self.collection_playlist_import = d.button('导入歌单', action=lambda: self.collection_import(True))
            actions.append(self.collection_create_button)
            actions.append(self.collection_playlist_import)
            self.collection_refresh_buttons()
            return page

        def collection_refresh_buttons(self):
            state = self.collection_state()
            ready = state.get('CanEdit', False) and not getattr(self, 'stopped', False)
            for attr in ('collection_create_button', 'collection_favorite_import', 'collection_playlist_import'):
                if widget := getattr(self, attr, None):
                    widget.set_sensitive(ready and ('import' not in attr or not state.get('Busy')))

        def collection_dialog(self, title, action=None, confirm='确认', *, body='', value=None, choices=(), danger=False):
            dialog = d.Adw.AlertDialog(heading=title, body=body)
            # Track and playlist names are literal text, not Pango markup.
            dialog.set_body_use_markup(False)
            extra = None
            read = lambda: None
            if value is not None:
                extra = d.Gtk.Entry(text=value, placeholder_text='歌单名称', max_length=60, activates_default=True)
                read = extra.get_text
            elif choices:
                extra = d.Gtk.DropDown.new_from_strings([name for _, name in choices])
                extra.set_enable_search(True)
                read = lambda: choices[extra.get_selected()][0] if extra.get_selected() < len(choices) else None
            if extra is not None:
                dialog.set_extra_child(extra)
            dialog.add_response('cancel', '取消' if action else '关闭')
            dialog.set_close_response('cancel')
            if action:
                dialog.add_response('confirm', confirm)
                dialog.set_response_appearance('confirm', d.Adw.ResponseAppearance.DESTRUCTIVE if danger else d.Adw.ResponseAppearance.SUGGESTED)
                dialog.connect('response::confirm', lambda *_: action(read()))
                if extra is not None and value is not None:
                    dialog.set_default_response('confirm')
                    dialog.set_response_enabled('confirm', bool(value.strip()))
                    extra.connect('changed', lambda entry: dialog.set_response_enabled('confirm', bool(entry.get_text().strip())))
            dialog.present(self.win)
            return dialog

        def collection_create(self):
            self.collection_dialog('新建歌单', lambda text: self.collection_open_action('collection-create', Text=text), '创建', value='')

        def collection_open_action(self, action, **data):
            if self.stopped:
                return
            self._collection_pending = (self.view, (self.collection_state().get('Detail') or {}).get('Serial'))
            self.send(action, **data)

        def open_playlist(self, n, title, source):
            self.collection_open_playlist(n)

        def collection_open_playlist(self, n):
            rows = self.state.get('Playlists') or ()
            if 0 <= n < len(rows):
                self.collection_open_action('collection-open', ID=rows[n]['ID'])

        def open(self, key):
            if key != getattr(self, 'view', None):
                self._collection_pending = None
                self._collection_browse = None
            return super().open(key)

        def collection_track_action(self, action, field, index, key, text=''):
            rows = self.state.get(field) or ()
            if not (0 <= index < len(rows) and rows[index]['ID'] == key):
                return
            data = dict(Index=index, ID=key, Text=text)
            op = f'collection-{action}-{field}'
            if action in ('title', 'album', 'artist'):
                self.collection_open_action(op, **data)
            else:
                self.send(op, **data)

        def collection_pick_playlist(self, run, track):
            if not self.collection_state().get('CanEdit'):
                return
            choices = [(p['ID'], p['Name']) for p in self.state.get('Playlists') or ()
                       if not p.get('ReadOnly', '只读' in p.get('Subtitle', ''))]
            if not choices:
                self.collection_dialog('添加到歌单', body='请先在“我的歌单”中新建本地歌单。')
                return
            self.collection_dialog('添加到歌单', lambda playlist: run('add', playlist) if playlist else None,
                                   '添加', body=track, choices=choices)

        def add_tracks(self, parent, indexes, favorite=True, add_to_queue=False):
            listing = super().add_tracks(parent, indexes, favorite=favorite, add_to_queue=add_to_queue)
            field = self.collection_list_field(parent)
            data = self.state.get(field) or ()
            detail = self.collection_state().get('Detail') or {}
            row = listing.get_first_child()
            index = 0
            while row is not None and index < len(data):
                track = data[index]
                key = track['ID']

                def run(action, text='', *, i=index, track_id=key, f=field):
                    self.collection_track_action(action, f, i, track_id, text)

                line = row.get_child()
                entries = [('曲目详情', lambda cb=run: cb('title'))]
                if field != 'Detail' or detail.get('Kind') != 'track':
                    title = line.get_first_child().get_next_sibling().get_first_child()
                    self.collection_link(title, lambda cb=run: cb('title'))
                if track.get('Album'):
                    entries.append(('专辑 · ' + track['Album'], lambda cb=run: cb('album')))
                artists = [name for name in track.get('Artist', '').split(' / ') if name and name != '未知艺人']
                if artists:
                    names = line.get_first_child().get_next_sibling()
                    if len(artists) == 1:
                        self.collection_link(names.get_last_child(), lambda cb=run, name=artists[0]: cb('artist', name))
                    entries.extend(('艺人 · ' + name, lambda cb=run, artist=name: cb('artist', artist)) for name in artists)
                if self.collection_state().get('CanEdit'):
                    star = d.button('切换本地收藏', 'non-starred-symbolic', lambda cb=run: cb('favorite'), 'flat')
                    star.set_valign(d.Gtk.Align.CENTER)
                    line.append(star)
                    row.collection_star = (key, star)
                    entries.append(('添加到歌单', lambda cb=run, title=track['Title']: self.collection_pick_playlist(cb, title)))
                    if field == 'Detail' and detail.get('Kind') == 'local':
                        entries.append(('从歌单移除', lambda cb=run, pl=detail['ID']: cb('remove', pl)))
                more = d.menu('曲目操作', entries)
                more.set_valign(d.Gtk.Align.CENTER)
                line.append(more)
                row = row.get_next_sibling()
                index += 1
            return listing

        def collection_list_field(self, parent):
            for field, attr in (('Favorites', 'favorite_area'), ('Results', 'search_results'),
                                ('History', 'history_area'), ('Detail', 'collection_detail_area')):
                if parent is getattr(self, attr, None):
                    return field
            return None

        def collection_link(self, label, action):
            # Replace only the label, keeping the approved title/artist layout.
            parent = label.get_parent()
            previous = label.get_prev_sibling()
            parent.remove(label)
            link = d.Gtk.Button(child=label, halign=d.Gtk.Align.START, css_classes=['flat', 'collection-link'])
            link.set_has_frame(False)
            link.set_cursor_from_name('pointer')
            link.connect('clicked', lambda *_: action())
            parent.insert_child_after(link, previous)

        def collection_render_detail(self, detail):
            if self.view != 'detail':
                self.return_page = self.view
            if old := self.screen.get_child_by_name('detail'):
                self.track_rows = [row for row in self.track_rows if not row.is_ancestor(old)]
                self.screen.remove(old)
            page, area, _ = self.page(detail['Title'])
            self.collection_detail_area = area
            actions = d.box(spacing=8)
            rows = self.indexes.get('Detail') or ()
            if rows:
                actions.append(d.button('播放全部', action=lambda: self.play_action('play-detail', 'Detail', 0), style='suggested-action'))
            kind, key, name = detail['Kind'], detail['ID'], detail['Title']
            if kind == 'local':
                actions.append(d.button('重命名', action=lambda: self.collection_dialog(
                    '重命名歌单', lambda text: self.send('collection-rename', ID=key, Text=text), '保存', value=name)))
                actions.append(d.button('删除歌单', action=lambda: self.collection_dialog(
                    '删除歌单', lambda _: self.send('collection-delete', ID=key), '删除', body=name, danger=True)))
            if kind == 'imported':
                self.collection_update_button = d.button('更新本地副本', action=lambda: self.collection_update(key))
                actions.append(self.collection_update_button)
            area.append(actions)
            subtitle = detail.get('Subtitle') or ''
            if kind == 'imported':
                subtitle = '只读本地副本' + (' · ' + subtitle if subtitle else '')
            if subtitle:
                area.append(d.label(subtitle, 'dim-label'))
            if detail.get('Loading'):
                area.append(d.label('正在读取详情…', 'dim-label'))
            elif not rows:
                area.append(d.label('没有曲目', 'dim-label'))
            albums = detail.get('Albums') or ()
            if albums:
                area.append(d.menu('查看艺人专辑', [(album['Name'], lambda a=album['ID']: self.collection_open_action(
                    'collection-artist-album', ID=a, Value=detail['Serial'])) for album in albums]))
            self.add_tracks(area, rows, favorite=False)
            self.screen.add_named(page, 'detail')
            self.open('detail')

        def collection_update(self, key):
            epoch = self.collection_state().get('AccountEpoch', 0)
            self.collection_dialog('更新本地副本', lambda _: self.send('collection-update', ID=key, Value=epoch), '更新',
                body='将用当前账号的线上歌单替换本地成员。不会修改线上歌单或本地收藏。', danger=True)

        def collection_import(self, playlists):
            state = self.collection_state()
            capability = 'Playlists' if playlists else 'Favorites'
            choices = [(row['ID'], row['Name']) for row in state.get('Sources') or () if row.get(capability)]
            heading = '导入歌单' if playlists else '导入收藏'
            if not choices:
                self.collection_dialog(heading, body='请先登录支持此操作的音源。')
                return
            epoch = state.get('AccountEpoch', 0)

            def run(source):
                if source is None or self.stopped:
                    return
                if playlists:
                    self._collection_browse = (source, state.get('RemoteGeneration'), self.view)
                    self.send('collection-browse', ID=source, Value=epoch)
                else:
                    self.send('collection-import-favorites', ID=source, Value=epoch)

            body = ('选择音源后浏览其在线歌单。导入后得到只读副本，不会修改在线歌单。' if playlists
                    else '只会新增或更新本地收藏，不会修改音源收藏。')
            self.collection_dialog(heading, run, '浏览' if playlists else '导入', body=body, choices=choices)

        def collection_choose_remote(self, state, source):
            remote = state.get('Remote') or ()
            choices = [(i, row['Name']) for i, row in enumerate(remote) if not row.get('Imported')]
            if not choices:
                self.collection_dialog('导入歌单', body='没有可导入的歌单。已导入的歌单可从“我的歌单”更新。')
                return

            def confirm(index):
                if index is not None:
                    self.send('collection-import', Index=index, ID=remote[index]['ID'], Text=source,
                              Value=state.get('RemoteGeneration', 0))

            self.collection_dialog('选择平台歌单', confirm, '导入', choices=choices)

        def collection_apply(self, old, state):
            """Apply after BoundDesign installs track indexes and rebuilds cards."""
            if self.stopped:
                return
            previous, current = old.get('Collections') or {}, state.get('Collections') or {}
            detail = current.get('Detail')
            pending = getattr(self, '_collection_pending', None)
            if detail and pending and pending[0] == self.view and detail['Serial'] != pending[1]:
                self._collection_pending = None
                self.collection_render_detail(detail)
            elif self.view == 'detail' and current:
                if detail is None and previous.get('Detail'):
                    self.open(self.return_page)
                elif detail and (detail != previous.get('Detail') or state.get('Detail') != old.get('Detail')):
                    self.collection_render_detail(detail)
            if pending and current.get('Notice') != previous.get('Notice') and (not detail or detail['Serial'] == pending[1]):
                self._collection_pending = None
            if current.get('Notice') != previous.get('Notice') and current.get('Status') and not current.get('Busy'):
                # Prefer the design's toast overlay when supplied by the shell.
                overlay = getattr(self, '_collection_toast_overlay', None)
                if overlay is None:
                    overlay = d.Adw.ToastOverlay()
                    self.win.set_content(None)
                    overlay.set_child(self.root)
                    self.win.set_content(overlay)
                    self._collection_toast_overlay = overlay
                overlay.add_toast(d.Adw.Toast(title=current['Status'], timeout=3))
            self.collection_refresh_buttons()
            if update := getattr(self, 'collection_update_button', None):
                available = any(s['ID'] == (detail or {}).get('Source') and s.get('Playlists')
                                for s in current.get('Sources') or ())
                update.set_sensitive(available and not current.get('Busy'))
            for row in self.track_rows:
                if star := getattr(row, 'collection_star', None):
                    liked = (current.get('FavoriteIDs') or {}).get(star[0], False)
                    widget = star[1]
                    icon = 'starred-symbolic' if liked else 'non-starred-symbolic'
                    if widget.get_icon_name() != icon:
                        widget.set_icon_name(icon)
                    widget.set_tooltip_text('取消本地收藏' if liked else '加入本地收藏')
            browse = getattr(self, '_collection_browse', None)
            if browse and not current.get('Busy') and browse[2] == self.view:
                self._collection_browse = None
                if current.get('RemoteGeneration') != browse[1] and current.get('RemoteSource') == browse[0]:
                    self.collection_choose_remote(current, browse[0])

        def apply(self, state):
            old = self.state
            result = super().apply(state)
            self.collection_apply(old, state)
            return result

    return LiveCollections
