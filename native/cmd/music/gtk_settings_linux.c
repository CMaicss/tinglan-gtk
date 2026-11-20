//go:build linux && gtk && cgo

#include <adwaita.h>
#include <stdint.h>

extern uintptr_t tlGoSettingsInit(uintptr_t session);
extern void tlGoSettingsDestroy(uintptr_t handle);
extern int tlGoSettingsHideOnClose(uintptr_t handle);
extern void tlGoSettingsAction(uintptr_t handle, char *action, char *value,
                              char *endpoint, char *model, char *key, int number);

typedef struct {
    uintptr_t handle;
    GtkWindow *window;
    GtkWidget *page, *source_tools, *empty;
    AdwPreferencesGroup *sources;
    GtkLabel *status;
    AdwComboRow *close, *quality, *mode, *theme;
    GtkSwitch *lyrics, *jev;
    GtkSpinButton *scale;
    AdwActionRow *font;
    GtkWidget *lyrics_import, *lyrics_remove, *jev_test;
    GtkEditable *endpoint, *model, *key;
    GtkLabel *jev_status;
    char *endpoint_value, *model_value;
    GtkCssProvider *style;
    gboolean syncing;
} Settings;

/* The only settings page belongs to the one application window. */
static Settings *settings;

static void send(Settings *s, const char *action, const char *value, int number)
{
    if (!s || s->syncing)
        return;
    tlGoSettingsAction(s->handle, (char *)action, (char *)value,
                       NULL, NULL, NULL, number);
}

static void button_clicked(GtkButton *button, Settings *s)
{
    send(s, g_object_get_data(G_OBJECT(button), "settings-action"),
         g_object_get_data(G_OBJECT(button), "source-id"), 0);
}

static GtkWidget *button(Settings *s, const char *label, const char *action,
                        GCallback callback)
{
    GtkWidget *w = gtk_button_new_with_label(label);
    gtk_widget_set_valign(w, GTK_ALIGN_CENTER);
    gtk_widget_add_css_class(w, "flat");
    g_object_set_data(G_OBJECT(w), "settings-action", (gpointer)action);
    g_signal_connect(w, "clicked", callback ? callback : G_CALLBACK(button_clicked), s);
    return w;
}

static char *markup_text(const char *text)
{
    char *valid = g_utf8_make_valid(text, -1);
    char *escaped = g_markup_escape_text(valid, -1);
    g_free(valid);
    return escaped;
}

static AdwActionRow *row(AdwPreferencesGroup *group, const char *title)
{
    AdwActionRow *r = ADW_ACTION_ROW(adw_action_row_new());
    char *markup = markup_text(title);
    adw_preferences_row_set_title(ADW_PREFERENCES_ROW(r), markup);
    g_free(markup);
    adw_preferences_group_add(group, GTK_WIDGET(r));
    return r;
}

static GtkWidget *action_row(Settings *s, AdwPreferencesGroup *group, const char *title,
                             const char *label, const char *action, GCallback callback)
{
    AdwActionRow *r = row(group, title);
    GtkWidget *b = button(s, label, action, callback);
    adw_action_row_add_suffix(r, b);
    adw_action_row_set_activatable_widget(r, b);
    return b;
}

static void selected(GObject *obj, GParamSpec *pspec, Settings *s)
{
    (void)pspec;
    send(s, g_object_get_data(obj, "settings-action"), NULL,
         adw_combo_row_get_selected(ADW_COMBO_ROW(obj)));
}

static AdwComboRow *combo(Settings *s, AdwPreferencesGroup *group, const char *title,
                          const char *action, const char *const *items)
{
    AdwComboRow *r = ADW_COMBO_ROW(adw_combo_row_new());
    GtkStringList *strings = gtk_string_list_new(items);
    adw_combo_row_set_model(r, G_LIST_MODEL(strings));
    g_object_unref(strings);
    adw_preferences_row_set_title(ADW_PREFERENCES_ROW(r), title);
    adw_preferences_group_add(group, GTK_WIDGET(r));
    g_object_set_data(G_OBJECT(r), "settings-action", (gpointer)action);
    g_signal_connect(r, "notify::selected", G_CALLBACK(selected), s);
    return r;
}

static void switched(GObject *obj, GParamSpec *pspec, Settings *s)
{
    (void)pspec;
    send(s, g_object_get_data(obj, "settings-action"), NULL,
         gtk_switch_get_active(GTK_SWITCH(obj)));
}

static GtkWidget *new_switch(const char *title)
{
    GtkWidget *w = gtk_switch_new();
    gtk_widget_set_valign(w, GTK_ALIGN_CENTER);
    gtk_accessible_update_property(GTK_ACCESSIBLE(w), GTK_ACCESSIBLE_PROPERTY_LABEL, title, -1);
    return w;
}

static GtkSwitch *switch_row(Settings *s, AdwPreferencesGroup *group, const char *title,
                             const char *action)
{
    AdwActionRow *r = row(group, title);
    GtkWidget *w = new_switch(title);
    adw_action_row_add_suffix(r, w);
    adw_action_row_set_activatable_widget(r, w);
    g_object_set_data(G_OBJECT(w), "settings-action", (gpointer)action);
    g_signal_connect(w, "notify::active", G_CALLBACK(switched), s);
    return GTK_SWITCH(w);
}

static void spin_changed(GtkSpinButton *spin, Settings *s)
{
    send(s, "font-size", NULL, gtk_spin_button_get_value_as_int(spin));
}

static void font_response(GtkDialog *dialog, int response, GtkWidget *page)
{
    Settings *s = settings;
    if (response == GTK_RESPONSE_OK && s && s->page == page) {
        PangoFontDescription *font = gtk_font_chooser_get_font_desc(GTK_FONT_CHOOSER(dialog));
        if (font) {
            send(s, "font-family", pango_font_description_get_family(font), 0);
            pango_font_description_free(font);
        }
    }
    gtk_window_destroy(GTK_WINDOW(dialog));
}

static void choose_font(GtkButton *b, Settings *s)
{
    (void)b;
    GtkWidget *dialog = gtk_font_chooser_dialog_new("选择系统字体", s->window);
    gtk_font_chooser_set_level(GTK_FONT_CHOOSER(dialog), GTK_FONT_CHOOSER_LEVEL_FAMILY);
    gtk_window_set_modal(GTK_WINDOW(dialog), TRUE);
    gtk_window_set_destroy_with_parent(GTK_WINDOW(dialog), TRUE);
    g_signal_connect_object(dialog, "response", G_CALLBACK(font_response), s->page, 0);
    gtk_window_present(GTK_WINDOW(dialog));
}

static void picker_response(GtkNativeDialog *dialog, int response, GtkWidget *page)
{
    Settings *s = settings;
    if (s && s->page == page) {
        gboolean lyrics = GPOINTER_TO_INT(g_object_get_data(G_OBJECT(dialog), "lyrics"));
        char *path = NULL;
        if (response == GTK_RESPONSE_ACCEPT) {
            GFile *file = gtk_file_chooser_get_file(GTK_FILE_CHOOSER(dialog));
            if (file) {
                path = g_file_get_path(file);
                g_object_unref(file);
            }
        }
        if (lyrics || path)
            send(s, lyrics ? "lyrics-picked" : "import-picked", path, 0);
        else if (response == GTK_RESPONSE_ACCEPT)
            send(s, "nonlocal-source", NULL, 0);
        g_free(path);
    }
    gtk_native_dialog_destroy(dialog);
    g_object_unref(dialog);
}

static void pick_file(GtkButton *button, Settings *s)
{
    const char *kind = g_object_get_data(G_OBJECT(button), "settings-action");
    gboolean folder = g_str_equal(kind, "folder"), lyrics = g_str_equal(kind, "lyrics");
    if (lyrics)
        send(s, "lyrics-pick-start", NULL, 0);
    GtkFileChooserNative *chooser = gtk_file_chooser_native_new(
        lyrics ? "导入本地歌词" : folder ? "从文件夹添加音源" : "添加音源包",
        s->window, folder ? GTK_FILE_CHOOSER_ACTION_SELECT_FOLDER : GTK_FILE_CHOOSER_ACTION_OPEN,
        lyrics ? "导入" : "添加", "取消");
    if (!folder && !lyrics) {
        GtkFileFilter *filter = gtk_file_filter_new();
        gtk_file_filter_set_name(filter, "音源包 (.zip)");
        gtk_file_filter_add_pattern(filter, "*.[zZ][iI][pP]");
        gtk_file_chooser_add_filter(GTK_FILE_CHOOSER(chooser), filter);
    }
    g_object_set_data(G_OBJECT(chooser), "lyrics", GINT_TO_POINTER(lyrics));
    gtk_native_dialog_set_modal(GTK_NATIVE_DIALOG(chooser), TRUE);
    g_signal_connect_object(chooser, "response", G_CALLBACK(picker_response), s->page, 0);
    gtk_native_dialog_show(GTK_NATIVE_DIALOG(chooser));
}

static void confirm_response(AdwMessageDialog *dialog, const char *response, GtkWidget *page)
{
    Settings *s = settings;
    if (s && s->page == page && g_str_equal(response, "confirm"))
        send(s, g_object_get_data(G_OBJECT(dialog), "settings-action"),
             g_object_get_data(G_OBJECT(dialog), "settings-value"), 1);
}

void tl_settings_confirm(const char *action, const char *value, const char *heading,
                         const char *body, const char *affirmative)
{
    Settings *s = settings;
    if (!s)
        return;
    char *valid_body = g_utf8_make_valid(body, -1);
    GtkWidget *widget = adw_message_dialog_new(s->window, heading, valid_body);
    g_free(valid_body);
    AdwMessageDialog *d = ADW_MESSAGE_DIALOG(widget);
    gtk_window_set_destroy_with_parent(GTK_WINDOW(d), TRUE);
    adw_message_dialog_add_responses(d, "cancel", "取消", "confirm", affirmative, NULL);
    adw_message_dialog_set_default_response(d, "cancel");
    adw_message_dialog_set_close_response(d, "cancel");
    adw_message_dialog_set_response_appearance(d, "confirm",
        g_str_has_prefix(action, "lyrics-clear") ? ADW_RESPONSE_DESTRUCTIVE : ADW_RESPONSE_SUGGESTED);
    g_object_set_data_full(G_OBJECT(d), "settings-action", g_strdup(action), g_free);
    g_object_set_data_full(G_OBJECT(d), "settings-value", g_strdup(value), g_free);
    g_signal_connect_object(d, "response", G_CALLBACK(confirm_response), s->page, 0);
    gtk_window_present(GTK_WINDOW(d));
}

static void jev_action(GtkButton *b, Settings *s)
{
    tlGoSettingsAction(s->handle, g_object_get_data(G_OBJECT(b), "settings-action"), NULL,
        (char *)gtk_editable_get_text(s->endpoint), (char *)gtk_editable_get_text(s->model),
        (char *)gtk_editable_get_text(s->key), 0);
}

static GtkWidget *plain_label(const char *label)
{
    GtkWidget *w = gtk_label_new(label);
    gtk_label_set_wrap(GTK_LABEL(w), TRUE);
    gtk_label_set_xalign(GTK_LABEL(w), 0);
    gtk_widget_add_css_class(w, "dim-label");
    return w;
}

static GtkBox *tab(AdwViewStack *tabs, const char *name, const char *title, const char *icon)
{
    GtkWidget *inside = gtk_box_new(GTK_ORIENTATION_VERTICAL, 14);
    GtkWidget *clamp = adw_clamp_new();
    gtk_widget_set_margin_start(clamp, 16);
    gtk_widget_set_margin_end(clamp, 16);
    gtk_widget_set_margin_top(clamp, 8);
    gtk_widget_set_margin_bottom(clamp, 20);
    adw_clamp_set_child(ADW_CLAMP(clamp), inside);
    adw_clamp_set_maximum_size(ADW_CLAMP(clamp), 720);
    GtkWidget *scroller = gtk_scrolled_window_new();
    gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(scroller), GTK_POLICY_NEVER, GTK_POLICY_AUTOMATIC);
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(scroller), clamp);
    adw_view_stack_add_titled_with_icon(tabs, scroller, name, title, icon);
    return GTK_BOX(inside);
}

static AdwPreferencesGroup *group(GtkBox *parent, const char *title)
{
    GtkWidget *g = adw_preferences_group_new();
    if (title)
        adw_preferences_group_set_title(ADW_PREFERENCES_GROUP(g), title);
    gtk_box_append(parent, g);
    return ADW_PREFERENCES_GROUP(g);
}

static GtkEditable *entry(AdwPreferencesGroup *g, const char *title, gboolean password)
{
    GtkWidget *w = password ? adw_password_entry_row_new() : adw_entry_row_new();
    adw_preferences_row_set_title(ADW_PREFERENCES_ROW(w), title);
    adw_preferences_group_add(g, w);
    return GTK_EDITABLE(w);
}

static gboolean hide_on_close(GtkWindow *window, GtkWidget *page)
{
    Settings *s = settings;
    if (!s || s->page != page || !tlGoSettingsHideOnClose(s->handle))
        return FALSE;
    gtk_widget_set_visible(GTK_WIDGET(window), FALSE);
    return TRUE; /* The shell must not tear down the model on a hide. */
}

static void quit_application(GSimpleAction *action, GVariant *parameter, GtkWindow *window)
{
    (void)action;
    (void)parameter;
    GtkApplication *app = gtk_window_get_application(window);
    if (app)
        g_application_quit(G_APPLICATION(app));
}

static void settings_mapped(GtkWidget *page, Settings *s)
{
    (void)page;
    send(s, "mapped", NULL, 0);
}

static void free_settings(gpointer data)
{
    Settings *s = data;
    if (settings == s)
        settings = NULL;
    tlGoSettingsDestroy(s->handle);
    g_free(s->endpoint_value);
    g_free(s->model_value);
    g_clear_object(&s->style);
    g_free(s);
}

GtkWidget *tl_make_settings(uintptr_t handle, GtkWindow *window)
{
    Settings *s = g_new0(Settings, 1);
    settings = s;
    s->handle = tlGoSettingsInit(handle);
    s->window = window;
    s->page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 8);
    g_object_set_data_full(G_OBJECT(s->page), "tinglan-settings", s, free_settings);
    g_signal_connect(s->page, "map", G_CALLBACK(settings_mapped), s);
    g_signal_connect_object(window, "close-request", G_CALLBACK(hide_on_close), s->page, 0);
    GSimpleAction *quit = g_simple_action_new("quit", NULL);
    g_signal_connect_object(quit, "activate", G_CALLBACK(quit_application), window, 0);
    g_action_map_add_action(G_ACTION_MAP(window), G_ACTION(quit));
    g_object_unref(quit);
    GtkApplication *app = gtk_window_get_application(window);
    if (app) {
        const char *accels[] = { "<Primary>q", NULL };
        gtk_application_set_accels_for_action(app, "win.quit", accels);
    }
    AdwViewStack *tabs = ADW_VIEW_STACK(adw_view_stack_new());
    GtkWidget *switcher = adw_view_switcher_new();
    adw_view_switcher_set_stack(ADW_VIEW_SWITCHER(switcher), tabs);
    adw_view_switcher_set_policy(ADW_VIEW_SWITCHER(switcher), ADW_VIEW_SWITCHER_POLICY_NARROW);
    gtk_widget_set_halign(switcher, GTK_ALIGN_START);
    gtk_widget_set_margin_top(switcher, 8);
    gtk_widget_set_margin_start(switcher, 12);
    gtk_box_append(GTK_BOX(s->page), switcher);
    GtkWidget *status = plain_label(NULL);
    s->status = GTK_LABEL(status);
    gtk_widget_set_margin_start(status, 16);
    gtk_widget_set_margin_end(status, 16);
    gtk_widget_set_visible(status, FALSE);
    gtk_box_append(GTK_BOX(s->page), status);
    gtk_widget_set_vexpand(GTK_WIDGET(tabs), TRUE);
    gtk_box_append(GTK_BOX(s->page), GTK_WIDGET(tabs));

    GtkBox *sources_tab = tab(tabs, "sources", "音源", "network-server-symbolic");
    GtkWidget *actions = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    s->source_tools = actions;
    GtkWidget *add = button(s, "添加音源", "zip", G_CALLBACK(pick_file));
    gtk_widget_remove_css_class(add, "flat");
    gtk_widget_add_css_class(add, "suggested-action");
    gtk_box_append(GTK_BOX(actions), add);
    GtkWidget *menu = gtk_menu_button_new();
    gtk_menu_button_set_icon_name(GTK_MENU_BUTTON(menu), "view-more-symbolic");
    gtk_widget_set_tooltip_text(menu, "音源操作");
    gtk_box_append(GTK_BOX(actions), menu);
    GtkWidget *popover = gtk_popover_new();
    GtkWidget *items = gtk_box_new(GTK_ORIENTATION_VERTICAL, 2);
    gtk_popover_set_child(GTK_POPOVER(popover), items);
    gtk_menu_button_set_popover(GTK_MENU_BUTTON(menu), popover);
    gtk_box_append(GTK_BOX(items), button(s,"从文件夹添加…","folder",G_CALLBACK(pick_file)));
    gtk_box_append(GTK_BOX(items), button(s,"打开音源文件夹","open-source-folder",NULL));
    gtk_box_append(GTK_BOX(items), button(s,"重新扫描音源","refresh",NULL));
    gtk_box_append(sources_tab, actions);
    s->empty = plain_label("未发现音源。可以添加受信任的外置音源。");
    gtk_box_append(sources_tab, s->empty);
    s->sources = group(sources_tab, NULL);

    AdwPreferencesGroup *play = group(tab(tabs, "play", "播放", "media-playback-start-symbolic"), NULL);
    s->close = combo(s, play, "关闭窗口时", "close", (const char *[]){"退出程序","隐藏窗口",NULL});
    s->quality = combo(s, play, "默认音质", "quality", (const char *[]){"标准","无损",NULL});
    s->mode = combo(s, play, "播放模式", "mode", (const char *[]){"顺序播放","列表循环","单曲循环","随机播放",NULL});
    s->lyrics = switch_row(s, play, "第三方歌词匹配", "lyrics-match");
    action_row(s, play, "清除网络缓存", "清除", "lyrics-clear-network", NULL);
    action_row(s, play, "清除本地导入", "清除…", "lyrics-clear-local", NULL);
    s->lyrics_import = action_row(s, play, "导入本地歌词", "导入…", "lyrics", G_CALLBACK(pick_file));
    s->lyrics_remove = action_row(s, play, "移除本曲导入", "移除", "lyrics-remove", NULL);

    AdwPreferencesGroup *appearance = group(tab(tabs, "appearance", "外观", "applications-graphics-symbolic"), NULL);
    AdwActionRow *scale = row(appearance, "字体大小（%）");
    GtkWidget *spin = gtk_spin_button_new_with_range(85, 120, 1);
    s->scale = GTK_SPIN_BUTTON(spin);
    gtk_widget_set_valign(spin, GTK_ALIGN_CENTER);
    gtk_accessible_update_property(GTK_ACCESSIBLE(spin), GTK_ACCESSIBLE_PROPERTY_LABEL, "字体大小（%）", -1);
    adw_action_row_add_suffix(scale, spin);
    adw_action_row_set_activatable_widget(scale, spin);
    g_signal_connect(spin, "value-changed", G_CALLBACK(spin_changed), s);
    s->font = row(appearance, "系统字体");
    GtkWidget *font = button(s, "选择…", NULL, G_CALLBACK(choose_font));
    adw_action_row_add_suffix(s->font, font);
    adw_action_row_set_activatable_widget(s->font, font);
    s->theme = combo(s, appearance, "主题", "theme", (const char *[]){"晨光","晴海","鼠尾草","暖砂","蔷薇","冰川","午夜蓝","深林","石墨","夜玫瑰","咖啡","曜石",NULL});
    action_row(s, appearance, "恢复默认外观", "恢复", "appearance-reset", NULL);

    GtkBox *advanced = tab(tabs, "advanced", "高级", "preferences-other-symbolic");
    AdwPreferencesGroup *jev = group(advanced, "决策模型（可选）");
    adw_preferences_group_set_description(jev, "仅用于收藏导入时辅助判断两首相似歌曲是否为同一录音。API Key 只会发送给你配置的地址。");
    s->jev = switch_row(s, jev, "启用模型辅助判断", "jev-enabled");
    s->endpoint = entry(jev, "API 地址", FALSE);
    s->key = entry(jev, "API Key", TRUE);
    s->model = entry(jev, "模型 ID", FALSE);
    s->jev_test = action_row(s, jev, "测试连接", "测试", "jev-test", G_CALLBACK(jev_action));
    action_row(s, jev, "保存配置", "保存", "jev-save", G_CALLBACK(jev_action));
    s->jev_status = GTK_LABEL(plain_label(NULL));
    gtk_box_append(advanced, GTK_WIDGET(s->jev_status));
    return s->page;
}

uintptr_t tl_settings_state(void)
{
    return settings ? settings->handle : 0;
}

int tl_settings_mapped(void)
{
    return settings && gtk_widget_get_mapped(settings->page);
}

void tl_settings_reset_sources(void)
{
    Settings *s = settings;
    if (!s)
        return;
    /* The group owns its children; track only app-added rows, not internals. */
    GPtrArray *rows = g_object_get_data(G_OBJECT(s->sources), "settings-rows");
    if (rows) {
        for (guint i = 0; i < rows->len; i++)
            adw_preferences_group_remove(s->sources, g_ptr_array_index(rows, i));
        g_ptr_array_set_size(rows, 0);
    }
    gtk_widget_set_visible(s->empty, TRUE);
}

static gboolean source_switch(GtkSwitch *w, gboolean state, Settings *s)
{
    (void)state;
    /* GTK updates the thumb before emitting state-set. Restore the last
     * committed state while the operation or consent prompt is pending. */
    g_signal_handlers_block_by_func(w, source_switch, s);
    gtk_switch_set_active(w, gtk_switch_get_state(w));
    g_signal_handlers_unblock_by_func(w, source_switch, s);
    send(s, "source-toggle", g_object_get_data(G_OBJECT(w), "source-id"), 0);
    return TRUE;
}

void tl_settings_add_source(const char *id, const char *name, const char *subtitle,
                            int enabled, const char *login, int signed_in, int can_login)
{
    Settings *s = settings;
    if (!s)
        return;
    GPtrArray *rows = g_object_get_data(G_OBJECT(s->sources), "settings-rows");
    if (!rows) {
        rows = g_ptr_array_new();
        g_object_set_data_full(G_OBJECT(s->sources), "settings-rows", rows, (GDestroyNotify)g_ptr_array_unref);
    }
    AdwActionRow *r = row(s->sources, name);
    g_ptr_array_add(rows, r);
    gtk_widget_set_visible(s->empty, FALSE);
    char *safe_subtitle = markup_text(subtitle);
    adw_action_row_set_subtitle(r, safe_subtitle);
    g_free(safe_subtitle);
    adw_action_row_add_prefix(r, gtk_image_new_from_icon_name("network-server-symbolic"));
    if (signed_in) {
        GtkWidget *logout = button(s,"退出账号","source-logout",NULL);
        g_object_set_data_full(G_OBJECT(logout), "source-id", g_strdup(id), g_free);
        gtk_widget_set_sensitive(logout, can_login);
        adw_action_row_add_suffix(r, logout);
    }
    if (*login) {
        GtkWidget *b = button(s,login,"source-login",NULL);
        g_object_set_data_full(G_OBJECT(b), "source-id", g_strdup(id), g_free);
        gtk_widget_set_sensitive(b, can_login);
        adw_action_row_add_suffix(r, b);
        adw_action_row_set_activatable_widget(r, b);
    }
    GtkWidget *sw = new_switch("启用音源");
    g_object_set_data_full(G_OBJECT(sw), "source-id", g_strdup(id), g_free);
    gtk_switch_set_active(GTK_SWITCH(sw), enabled);
    g_signal_connect(sw, "state-set", G_CALLBACK(source_switch), s);
    adw_action_row_add_suffix(r, sw);
}

static void set_label(GtkLabel *label, const char *text)
{
    if (g_strcmp0(gtk_label_get_text(label), text) != 0) {
        char *valid = g_utf8_make_valid(text, -1);
        gtk_label_set_text(label, valid);
        g_free(valid);
    }
    gtk_widget_set_visible(GTK_WIDGET(label), text && *text);
}

void tl_settings_status(const char *text, int busy)
{
    Settings *s = settings;
    if (!s)
        return;
    set_label(s->status, text);
    gtk_widget_set_sensitive(s->source_tools, !busy);
    gtk_widget_set_sensitive(GTK_WIDGET(s->sources), !busy);
}

void tl_settings_preferences(int close, int quality, int mode, int lyrics, int scale,
                             int theme, int jev, const char *font, int current)
{
    Settings *s = settings;
    if (!s)
        return;
    s->syncing = TRUE;
    adw_combo_row_set_selected(s->close, close);
    adw_combo_row_set_selected(s->quality, quality);
    adw_combo_row_set_selected(s->mode, mode);
    adw_combo_row_set_selected(s->theme, theme);
    gtk_switch_set_active(s->lyrics, lyrics);
    gtk_switch_set_active(s->jev, jev);
    gtk_spin_button_set_value(s->scale, scale);
    char *safe_font = markup_text(font);
    if (g_strcmp0(adw_action_row_get_subtitle(s->font), safe_font) != 0)
        adw_action_row_set_subtitle(s->font, safe_font);
    g_free(safe_font);
    gtk_widget_set_sensitive(s->lyrics_import, current);
    gtk_widget_set_sensitive(s->lyrics_remove, current);
    s->syncing = FALSE;
}

static void sync_entry(GtkEditable *entry, char **previous, const char *next)
{
    if (g_strcmp0(*previous, next) == 0)
        return; /* Never overwrite an in-progress edit on playback wakes. */
    g_free(*previous);
    *previous = g_strdup(next);
    gtk_editable_set_text(entry, next);
}

void tl_settings_jev(const char *endpoint, const char *model, const char *status, int testing)
{
    Settings *s = settings;
    if (!s)
        return;
    if (testing < 0) {
        gtk_editable_set_text(s->key, "");
        return;
    }
    sync_entry(s->endpoint, &s->endpoint_value, endpoint);
    sync_entry(s->model, &s->model_value, model);
    set_label(s->jev_status, status);
    gtk_widget_set_sensitive(s->jev_test, !testing);
}

void tl_settings_apply_appearance(const char *css, int scheme)
{
    Settings *s = settings;
    if (!s)
        return;
    if (!s->style) {
        s->style = gtk_css_provider_new();
        gtk_widget_add_css_class(GTK_WIDGET(s->window), "tinglan-settings-style");
        gtk_style_context_add_provider_for_display(gtk_widget_get_display(GTK_WIDGET(s->window)),
            GTK_STYLE_PROVIDER(s->style), GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
    }
    gtk_css_provider_load_from_data(s->style, css, -1);
    adw_style_manager_set_color_scheme(adw_style_manager_get_default(),
        scheme == 2 ? ADW_COLOR_SCHEME_FORCE_DARK : scheme == 1 ? ADW_COLOR_SCHEME_FORCE_LIGHT : ADW_COLOR_SCHEME_DEFAULT);
}
