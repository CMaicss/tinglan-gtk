"""Bind the design's settings pages to the shared model over the local bridge."""
import json


def settings_mixin(d):
    Gtk, Adw = d.Gtk, d.Adw

    class LiveSettings:
        def settings_build(self):
            page, area, _ = self.page('设置')
            self.settings_state = {}
            self.settings_rows = {}
            self.settings_row_actions = {}
            self.settings_controls = {}
            self.settings_syncing = False
            self.settings_save_serial = 0
            self.settings_save_draft = None
            self.settings_appearance = None
            self.settings_css = Gtk.CssProvider()
            self.win.add_css_class('tinglan-live-settings')
            Gtk.StyleContext.add_provider_for_display(self.win.get_display(), self.settings_css,
                                                       Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION + 1)

            tabs = Adw.ViewStack()
            tabs.set_hhomogeneous(False)
            area.append(Adw.ViewSwitcher(stack=tabs, policy=Adw.ViewSwitcherPolicy.NARROW, halign=Gtk.Align.START))
            area.append(tabs)
            groups = {}
            for key, name, glyph in [('sources', '音源', 'network-server-symbolic'),
                                     ('play', '播放', 'media-playback-start-symbolic'),
                                     ('appearance', '外观', 'applications-graphics-symbolic'),
                                     ('advanced', '高级', 'preferences-other-symbolic')]:
                slot = d.margin(d.box(True, 14), 0, 8)
                tabs.add_titled_with_icon(slot, key, name, glyph)
                group = Adw.PreferencesGroup()
                slot.append(group)
                groups[key] = (slot, group)
            slot, self.settings_sources = groups['sources']
            actions = d.box(spacing=8)
            actions.append(d.button('添加音源', action=lambda: self.settings_pick_source(False), style='suggested-action'))
            actions.append(d.menu('音源操作', [
                ('从文件夹添加…', lambda: self.settings_pick_source(True)),
                ('打开音源文件夹', lambda: self.send('settings-source-folder')),
                ('刷新列表', lambda: self.send('settings-source-refresh')),
            ]))
            slot.prepend(actions)
            self.settings_source_toolbar = actions
            self.settings_source_status = d.label('正在读取音源…', 'dim-label')
            self.settings_source_status.set_wrap(True)
            slot.append(self.settings_source_status)

            _, play = groups['play']
            self.settings_combo(play, '关闭窗口时', 'close', ['退出程序', '隐藏窗口'])
            self.settings_combo(play, '默认音质', 'quality', ['标准', '无损'])
            capacity = Adw.SpinRow.new_with_range(0, 16384, 128)
            capacity.set_title('音频缓存上限（MiB）')
            capacity.set_subtitle('已缓存 0 MiB · 0 为关闭')
            capacity.connect('notify::value', lambda w, _: self.settings_pref('audio-cache', round(w.get_value())))
            play.add(capacity)
            self.settings_controls['audio-cache'] = capacity
            self.settings_combo(play, '播放模式', 'mode', ['顺序播放', '列表循环', '单曲循环', '随机播放'])
            self.settings_switch(play, '第三方歌词匹配', 'lyrics-match')
            self.settings_lyrics_actions = [
                self.settings_action(play, '清除网络缓存', '清除', lambda: self.settings_clear_lyrics(False)),
                self.settings_action(play, '清除本地导入', '清除', lambda: self.settings_clear_lyrics(True)),
                self.settings_action(play, '导入本地歌词', '导入', self.settings_pick_lyrics),
                self.settings_action(play, '移除本曲导入', '移除', self.settings_remove_lyrics),
            ]
            _, self.settings_cover_clear = self.settings_action(play, '封面缓存', '清除', self.settings_clear_covers)
            self.settings_play_status = d.label('', 'dim-label')
            self.settings_play_status.set_wrap(True)
            groups['play'][0].append(self.settings_play_status)

            _, appearance = groups['appearance']
            size = Adw.SpinRow.new_with_range(85, 120, 1)
            size.set_title('字体大小')
            size.set_value(100)
            size.connect('notify::value', lambda w, _: self.settings_pref('font-size', round(w.get_value())))
            appearance.add(size)
            self.settings_controls['font-size'] = size
            self.settings_font_row, _ = self.settings_action(appearance, '系统字体', '选择', self.settings_pick_font)
            self.settings_combo(appearance, '主题', 'theme', d.THEME_OPTIONS)
            self.settings_action(appearance, '恢复默认外观', '恢复', lambda: self.send('settings-appearance-reset'))

            _, advanced = groups['advanced']
            self.settings_switch(advanced, '启用模型辅助判断', 'jev-enabled')
            self.settings_jev_fields = {
                'Endpoint': Adw.EntryRow(title='API 地址'),
                'Key': Adw.PasswordEntryRow(title='API Key（留空保留已保存的密钥）'),
                'Model': Adw.EntryRow(title='模型 ID'),
            }
            for row in self.settings_jev_fields.values():
                advanced.add(row)
            _, self.settings_test_button = self.settings_action(advanced, '测试连接', '测试', lambda: self.settings_jev(False))
            _, self.settings_save_button = self.settings_action(advanced, '保存配置', '保存', lambda: self.settings_jev(True))
            self.settings_jev_status = d.label('', 'dim-label')
            self.settings_jev_status.set_wrap(True)
            groups['advanced'][0].append(self.settings_jev_status)
            for group in (self.settings_source_toolbar, play, appearance, advanced):
                group.set_sensitive(False)
            self.settings_initial_groups = (play, appearance, advanced)
            return page

        def settings_action(self, group, title, action_text, callback):
            row = Adw.ActionRow(title=title)
            button = d.button(action_text, action=callback, style='flat')
            button.set_valign(Gtk.Align.CENTER)
            row.add_suffix(button)
            row.set_activatable_widget(button)
            group.add(row)
            return row, button

        def settings_combo(self, group, title, key, options):
            row = Adw.ComboRow(title=title, model=Gtk.StringList.new(options))
            group.add(row)
            row.connect('notify::selected', lambda w, _: self.settings_pref(key, w.get_selected()))
            self.settings_controls[key] = row

        def settings_switch(self, group, title, key):
            row = Adw.SwitchRow(title=title)
            group.add(row)
            row.connect('notify::active', lambda w, _: self.settings_pref(key, int(w.get_active())))
            self.settings_controls[key] = row

        def settings_pref(self, key, index):
            if not self.settings_syncing:
                self.send('settings-' + key, Index=index)

        def settings_confirm(self, title, text, action, confirm, *, destructive=False):
            dialog = Adw.AlertDialog(heading=title, body=text)
            dialog.add_response('cancel', '取消')
            dialog.add_response('confirm', action)
            dialog.set_close_response('cancel')
            dialog.set_default_response('cancel')
            dialog.set_response_appearance('confirm', Adw.ResponseAppearance.DESTRUCTIVE if destructive
                                           else Adw.ResponseAppearance.SUGGESTED)
            dialog.connect('response::confirm', lambda *_: confirm())
            dialog.present(self.win)

        def settings_pick(self, title, callback, *, folder=False, patterns=()):
            dialog = Gtk.FileDialog(title=title)
            if patterns:
                filt = Gtk.FileFilter()
                for pattern in patterns:
                    filt.add_pattern(pattern)
                dialog.set_default_filter(filt)
            finish = dialog.select_folder_finish if folder else dialog.open_finish

            def done(_, result):
                try:
                    path = finish(result).get_path()
                except d.GLib.Error:
                    return
                if path:
                    callback(path)
                elif title == '导入本地歌词':
                    self.settings_play_status.set_text('请选择本机文件')
                else:
                    self.settings_source_status.set_text('请选择本机文件或文件夹')

            (dialog.select_folder if folder else dialog.open)(self.win, None, done)

        def settings_pick_source(self, folder):
            def picked(path):
                self.settings_confirm('添加外置音源', '为了识别音源，听澜需要运行其中的程序一次。'
                                      '程序拥有你的用户权限，只添加来自可信来源的音源。\n\n' + path,
                                      '运行并添加', lambda: self.send('settings-source-install', Text=path, Index=1))
            self.settings_pick('从文件夹添加音源' if folder else '添加音源包', picked, folder=folder,
                               patterns=() if folder else ('*.[zZ][iI][pP]',))

        def settings_pick_lyrics(self):
            target = self.settings_state.get('LyricsTarget')
            if target:
                self.settings_pick('导入本地歌词', lambda path: self.send('settings-lyrics-import', ID=target, Text=path))

        def settings_clear_lyrics(self, local):
            label, op = ('本地导入歌词', 'local') if local else ('网络歌词缓存', 'network')
            self.settings_confirm('清除' + label, '不会影响收藏或播放队列。', '清除',
                                  lambda: self.send('settings-lyrics-clear-' + op, Index=1), destructive=True)

        def settings_clear_covers(self):
            self.settings_confirm('清除封面缓存', '只清除缓存图片，不影响收藏、歌单或歌词。再次查看时会重新加载。', '清除',
                                  lambda: self.send('art-clear', Index=1), destructive=True)

        def settings_remove_lyrics(self):
            if target := self.settings_state.get('LyricsTarget'):
                self.settings_confirm('移除本曲导入歌词', '只移除当前歌曲的本地导入。', '移除',
                                      lambda: self.send('settings-lyrics-remove', ID=target, Index=1), destructive=True)

        def settings_pick_font(self):
            dialog = Gtk.FontDialog(title='选择字体')
            current = self.settings_state.get('FontFamily')
            initial = d.Pango.FontDescription.from_string(current) if current else None

            def done(dialog, result):
                try:
                    font = dialog.choose_font_finish(result)
                except d.GLib.Error:
                    return
                self.send('settings-font-family', Text=font.get_family() or '')

            dialog.choose_font(self.win, initial, None, done)

        def settings_jev(self, save):
            draft = {name: field.get_text() for name, field in self.settings_jev_fields.items()}
            if save:
                self.settings_save_serial += 1
                self.settings_save_draft = draft
            self.send('settings-jev-' + ('save' if save else 'test'), Index=self.settings_save_serial,
                      Text=json.dumps(draft, ensure_ascii=False))

        def settings_toggle_source(self, source_id, switch):
            if self.settings_syncing:
                return
            row = next((r for r in self.settings_state.get('Sources', ()) if r['ID'] == source_id), None)
            if row is None:
                return
            enabled = switch.get_active()
            if enabled == bool(row.get('Enabled')):
                return
            self.settings_syncing = True
            switch.set_active(bool(row.get('Enabled')))
            self.settings_syncing = False
            if enabled and row.get('External'):
                detail = ' '.join(filter(None, (row['Name'], row.get('Version'))))
                if caps := row.get('Capabilities'):
                    detail += '\n能力：' + caps
                self.settings_confirm('启用外置音源', detail + '\n\n外置音源以你的用户权限运行。', '启用',
                                      lambda: self.send('settings-source-enable', ID=source_id, Index=1))
            else:
                self.send('settings-source-' + ('enable' if enabled else 'disable'), ID=source_id)

        def settings_render_sources(self, state):
            rows = state.get('Sources') or []
            ids = {r['ID'] for r in rows}
            for source_id in self.settings_rows.keys() - ids:
                row, _ = self.settings_rows.pop(source_id)
                self.settings_row_actions.pop(source_id)
                self.settings_sources.remove(row)
            busy = state.get('SourceBusy', False)
            account_busy = state.get('AccountBusy', False)
            for data in rows:
                source_id = data['ID']
                if source_id not in self.settings_rows:
                    row = Adw.ActionRow(use_markup=False)
                    row.add_prefix(d.icon('network-server-symbolic'))
                    switch = Gtk.Switch(valign=Gtk.Align.CENTER, tooltip_text='启用音源')
                    login = d.button('登录', action=lambda i=source_id: self.send('settings-source-login', ID=i), style='flat')
                    logout = d.button('退出登录', action=lambda i=source_id: self.settings_confirm(
                        '退出音源登录', '本地收藏仍可用。', '退出登录',
                        lambda: self.send('settings-source-logout', ID=i, Index=1)), style='flat')
                    for widget in (login, logout, switch):
                        widget.set_valign(Gtk.Align.CENTER)
                        row.add_suffix(widget)
                    switch.connect('notify::active', lambda w, _, i=source_id: self.settings_toggle_source(i, w))
                    self.settings_rows[source_id] = (row, switch)
                    self.settings_row_actions[source_id] = (login, logout)
                    self.settings_sources.add(row)
                row, switch = self.settings_rows[source_id]
                login, logout = self.settings_row_actions[source_id]
                enabled, signed_in = bool(data.get('Enabled')), bool(data.get('SignedIn'))
                login_label = data.get('LoginLabel') or ''
                subtitle = [data.get('Version'), '已启用' if enabled else '已停用']
                if login_label:
                    subtitle.append('正在登录…' if login_label == '取消登录' else ('已登录' if signed_in else '未登录'))
                row.set_title(data['Name'])
                row.set_subtitle(' · '.join(filter(None, subtitle)))
                switch.set_active(enabled)
                switch.set_sensitive(not busy and not account_busy)
                login.set_visible(bool(login_label))
                login.set_label(login_label)
                login.set_sensitive(not busy and bool(data.get('CanLogin')))
                logout.set_visible(signed_in and login_label != '取消登录')
                logout.set_sensitive(not busy and not account_busy)
            self.settings_source_toolbar.set_sensitive(bool(state.get('SourceReady')) and not busy and not account_busy)
            self.settings_source_status.set_text(state.get('Message') or (state.get('Status') if rows else '没有已安装的音源') or '')

        def settings_style(self, state):
            appearance = (state.get('Theme', 0), state.get('FontSize', 100), state.get('FontFamily', ''))
            if appearance == self.settings_appearance:
                return
            self.settings_appearance = appearance
            theme, size, font = appearance
            if not (getattr(self.args, 'dark', False) or getattr(self.args, 'light', False)):
                schemes = (Adw.ColorScheme.DEFAULT, Adw.ColorScheme.FORCE_LIGHT, Adw.ColorScheme.FORCE_DARK)
                Adw.StyleManager.get_default().set_color_scheme(schemes[theme] if 0 <= theme < len(schemes) else schemes[0])
            family = ''.join(f'\\{ord(ch):x} ' for ch in font)
            extra = f'font-family: "{family}";' if family else ''
            self.settings_css.load_from_string(f'window.tinglan-live-settings {{ font-size: {int(size)}%; {extra} }}')

        def settings_apply(self, snapshot):
            state = snapshot.get('Settings')
            if not isinstance(state, dict):
                return
            old, self.settings_state = self.settings_state, state
            self.settings_syncing = True
            try:
                for group in self.settings_initial_groups:
                    group.set_sensitive(True)
                if any(old.get(k) != state.get(k) for k in ('Sources', 'SourceBusy', 'SourceReady', 'AccountBusy', 'Message', 'Status')):
                    self.settings_render_sources(state)
                for control, field in [('close', 'CloseToTray'), ('quality', 'Lossless'), ('mode', 'Mode'), ('theme', 'Theme')]:
                    self.settings_controls[control].set_selected(int(state.get(field, 0)))
                for control, field in [('lyrics-match', 'ThirdPartyLyrics'), ('jev-enabled', 'JevEnabled')]:
                    self.settings_controls[control].set_active(bool(state.get(field)))
                self.settings_controls['font-size'].set_value(state.get('FontSize', 100))
                capacity = self.settings_controls['audio-cache']
                limit = state.get('AudioCacheMiB', 1024)
                used = state.get('AudioCacheBytes', 0) / (1024 * 1024)
                capacity.set_value(limit)
                capacity.set_subtitle(f'已缓存 {used:.1f} / {limit} MiB · 0 为关闭')
                self.settings_font_row.set_subtitle(state.get('FontFamily') or '默认')
                self.settings_style(state)
                store = bool(state.get('StoreReady'))
                self.settings_cover_clear.set_sensitive(store and not (snapshot.get('Art') or {}).get('Clearing'))
                for i, (_, button) in enumerate(self.settings_lyrics_actions):
                    button.set_sensitive(store and (i < 2 or bool(state.get('LyricsTarget'))))
                stats = state.get('LyricStats') or {}
                summary = f"网络缓存：{stats.get('Network', 0)} · 本地导入：{stats.get('Local', 0)}"
                self.settings_play_status.set_text(summary + ('\n' + state['Status'] if state.get('Status') else ''))
                self.settings_apply_jev(old, state, store)
            finally:
                self.settings_syncing = False

        def settings_apply_jev(self, old, state, store):
            saved = (self.settings_save_draft is not None and state.get('JevSaved') == self.settings_save_serial)
            for form in ('Endpoint', 'Model'):
                old_value = old.get('Jev' + form, '')
                entry = self.settings_jev_fields[form]
                if entry.get_text() == old_value or (saved and entry.get_text() == self.settings_save_draft[form]):
                    entry.set_text(state.get('Jev' + form, ''))
            if saved:
                key = self.settings_jev_fields['Key']
                if key.get_text() == self.settings_save_draft['Key']:
                    key.set_text('')
                self.settings_save_draft = None
            self.settings_test_button.set_sensitive(store and not state.get('JevTesting'))
            self.settings_save_button.set_sensitive(store and not state.get('JevTesting'))
            self.settings_jev_status.set_text(state.get('JevStatus') or state.get('Status') or '')

    return LiveSettings
