//go:build linux && gtk && cgo

#include "gtk_linux.h"

#include <adwaita.h>
#include <math.h>
#include <string.h>

extern void tlGoReady(uintptr_t handle);
extern void tlGoWake(uintptr_t handle);
extern void tlGoSearch(uintptr_t handle, char *query);
extern void tlGoAction(uintptr_t handle, int action, double value);
extern void tlGoRow(uintptr_t handle, unsigned int position);
extern void tlGoHistoryRow(uintptr_t handle, unsigned int position);
extern void tlGoQueueAction(uintptr_t handle, int action, unsigned int position);
extern void tlGoMore(uintptr_t handle);
extern void tlGoMapped(uintptr_t handle);
extern void tlGoClosing(uintptr_t handle);
extern GtkWidget *tl_make_library(uintptr_t handle, GtkWindow *window, int page);
extern GtkWidget *tl_make_settings(uintptr_t handle, GtkWindow *window);
extern GtkWidget *tl_make_lyrics(uintptr_t handle, GtkWindow *window);

enum { PREVIOUS = 1, TOGGLE_PLAYBACK, NEXT, SEEK, VOLUME, PLAY_MODE, QUALITY, LYRICS };

typedef struct {
    uintptr_t handle;
    GtkWindow *window;
    GtkLabel *status, *title, *artist, *elapsed, *duration;
    GtkImage *logo, *cover;
    GtkScaleButton *volume;
    GtkMenuButton *mode_menu;
    GSimpleAction *mode_action, *quality_action;
    GtkWidget *sidebar, *identity, *tools, *compact_brand, *nav_toggle, *stage_back;
    GtkWidget *progress, *center;
    GtkBox *transport_surface;
    GtkMenuButton *compact_tools;
    AdwFlap *queue_flap;
    GtkToggleButton *queue_toggle;
    const char *before_lyrics;
    GtkImage *compact_logo;
    gboolean stage, compact;
    GtkCenterBox *transport_layout;
    AdwFlap *navigation;
    gboolean volume_sync;
    GtkStack *search_body, *pages, *history_page;
    GtkLabel *page_title;
    GtkStringList *history_rows;
    AdwStatusPage *empty_search;
    GtkButton *play;
    GtkRange *position;
    GtkStringList *rows; /* Owned by the list view's selection model. */
    GtkWidget *queue_panel;
    GtkButton *queue_clear;
    GtkStringList *queue_rows;
    GtkSingleSelection *queue_selection;
    GtkAdjustment *scroll;
    double scroll_value, scroll_upper, scroll_page, seek_start;
    unsigned int ignore_scroll;
    gboolean syncing, seeking, closing;
} TinglanUI;

/* Only read or written on the GTK thread. Never used by an idle source. */
static TinglanUI *active;

static gboolean wake(gpointer data)
{
    tlGoWake((uintptr_t)data);
    return G_SOURCE_REMOVE;
}

unsigned int tl_post(uintptr_t handle)
{
    return g_idle_add_full(G_PRIORITY_DEFAULT_IDLE, wake, (gpointer)handle, NULL);
}

void tl_cancel(unsigned int id)
{
    if (id != 0)
        g_source_remove(id);
}

static GtkWidget *label(const char *text, const char *style)
{
    GtkWidget *widget = gtk_label_new(text);
    gtk_label_set_xalign(GTK_LABEL(widget), 0);
    gtk_label_set_ellipsize(GTK_LABEL(widget), PANGO_ELLIPSIZE_END);
    if (style)
        gtk_widget_add_css_class(widget, style);
    return widget;
}

static void pad(GtkWidget *widget, int vertical, int horizontal)
{
    gtk_widget_set_margin_top(widget, vertical);
    gtk_widget_set_margin_bottom(widget, vertical);
    gtk_widget_set_margin_start(widget, horizontal);
    gtk_widget_set_margin_end(widget, horizontal);
}

static void accessible_label(GtkWidget *widget, const char *text)
{
    gtk_accessible_update_property(GTK_ACCESSIBLE(widget),
                                   GTK_ACCESSIBLE_PROPERTY_LABEL, text, -1);
}

static void submit_search(GtkSearchEntry *entry, TinglanUI *ui)
{
    /* The GTK buffer may change during the callback; pass a snapshot. */
    char *query = g_strdup(gtk_editable_get_text(GTK_EDITABLE(entry)));
    tlGoSearch(ui->handle, query);
    g_free(query);
}

static void search_clicked(GtkButton *button, GtkSearchEntry *entry)
{
    (void)button;
    g_signal_emit_by_name(entry, "activate");
}

static void remove_queue_row(GtkButton *button, GtkListItem *item)
{
    (void)button;
    unsigned int pos = gtk_list_item_get_position(item);
    if (active && pos != GTK_INVALID_LIST_POSITION)
        tlGoQueueAction(active->handle, 2, pos);
}

static void setup_row(GtkSignalListItemFactory *factory, GtkListItem *item,
                      gpointer data)
{
    (void)factory;
    GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 2);
    GtkWidget *title = label(NULL, NULL);
    GtkWidget *artist = label(NULL, "dim-label");
    pad(box, 8, 12);
    gtk_box_append(GTK_BOX(box), title);
    gtk_box_append(GTK_BOX(box), artist);
    g_object_set_data(G_OBJECT(item), "tl-title", title);
    g_object_set_data(G_OBJECT(item), "tl-artist", artist);
    if (data) {
        GtkWidget *line = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 4);
        GtkWidget *remove = gtk_button_new_from_icon_name("list-remove-symbolic");
        gtk_widget_add_css_class(remove, "flat");
        gtk_widget_set_tooltip_text(remove, "移除曲目");
        accessible_label(remove, "移除曲目");
        gtk_widget_set_valign(remove, GTK_ALIGN_CENTER);
        g_signal_connect_object(remove, "clicked", G_CALLBACK(remove_queue_row), item, 0);
        gtk_widget_set_hexpand(box, TRUE);
        gtk_box_append(GTK_BOX(line), box);
        gtk_box_append(GTK_BOX(line), remove);
        gtk_list_item_set_child(item, line);
    } else {
        gtk_list_item_set_child(item, box);
    }
}

static void bind_row(GtkSignalListItemFactory *factory, GtkListItem *item,
                     gpointer data)
{
    (void)factory;
    (void)data;
    const char *text = gtk_string_object_get_string(gtk_list_item_get_item(item));
    const char *separator = strchr(text, '\n');
    GtkWidget *title = g_object_get_data(G_OBJECT(item), "tl-title");
    GtkWidget *artist = g_object_get_data(G_OBJECT(item), "tl-artist");
    char *first = separator ? g_strndup(text, separator - text) : NULL;
    gtk_label_set_text(GTK_LABEL(title), first ? first : text);
    gtk_label_set_text(GTK_LABEL(artist), separator ? separator + 1 : "");
    g_free(first);
}

static void activate_row(GtkListView *view, unsigned int position, TinglanUI *ui)
{
    (void)view;
    tlGoRow(ui->handle, position);
}

static void scroll_changed(GtkAdjustment *adjustment, TinglanUI *ui)
{
    double value = gtk_adjustment_get_value(adjustment);
    double upper = gtk_adjustment_get_upper(adjustment);
    double page = gtk_adjustment_get_page_size(adjustment);
    gboolean forward = value > ui->scroll_value && upper == ui->scroll_upper &&
                       page == ui->scroll_page;
    ui->scroll_value = value;
    ui->scroll_upper = upper;
    ui->scroll_page = page;
    if (!ui->ignore_scroll && forward && page > 0 && upper > page &&
        upper - page - value <= MIN(120.0, page / 4))
        tlGoMore(ui->handle);
}

static void transport_clicked(GtkButton *button, TinglanUI *ui)
{
    int action = GPOINTER_TO_INT(g_object_get_data(G_OBJECT(button), "tl-action"));
    tlGoAction(ui->handle, action, 0);
}

static GtkWidget *transport_button(TinglanUI *ui, const char *icon,
                                   const char *text, int action)
{
    GtkWidget *button = gtk_button_new_from_icon_name(icon);
    gtk_widget_set_tooltip_text(button, text);
    accessible_label(button, text);
    gtk_widget_add_css_class(button, "flat");
    g_object_set_data(G_OBJECT(button), "tl-action", GINT_TO_POINTER(action));
    g_signal_connect(button, "clicked", G_CALLBACK(transport_clicked), ui);
    return button;
}

static void position_changed(GtkRange *range, TinglanUI *ui)
{
    if (!ui->syncing && !ui->seeking)
        tlGoAction(ui->handle, SEEK, gtk_range_get_value(range));
}

/* Observe the pointer sequence in capture phase, without taking it away
 * from GtkScale. Send a single seek on release rather than on every motion. */
static gboolean seek_event(GtkEventControllerLegacy *controller, GdkEvent *event,
                           TinglanUI *ui)
{
    (void)controller;
    switch (gdk_event_get_event_type(event)) {
    case GDK_BUTTON_PRESS:
        if (gdk_button_event_get_button(event) != GDK_BUTTON_PRIMARY)
            break;
        G_GNUC_FALLTHROUGH;
    case GDK_TOUCH_BEGIN:
        if (!ui->seeking) {
            ui->seeking = TRUE;
            ui->seek_start = gtk_range_get_value(ui->position);
        }
        break;
    case GDK_BUTTON_RELEASE:
        if (gdk_button_event_get_button(event) != GDK_BUTTON_PRIMARY)
            break;
        G_GNUC_FALLTHROUGH;
    case GDK_TOUCH_END:
        if (ui->seeking) {
            double value = gtk_range_get_value(ui->position);
            ui->seeking = FALSE;
            if (value != ui->seek_start)
                tlGoAction(ui->handle, SEEK, value);
        }
        break;
    case GDK_GRAB_BROKEN:
    case GDK_TOUCH_CANCEL:
        ui->seeking = FALSE;
        break;
    default:
        break;
    }
    return GDK_EVENT_PROPAGATE;
}

static void choose_page(GtkListBox *list, GtkListBoxRow *row, TinglanUI *ui)
{
    (void)list;
    if (!row)
        return;
    const char *pages[] = {"search", "favorites", "playlists", "history"};
    int index = gtk_list_box_row_get_index(row);
    if (index >= 0 && (unsigned int)index < G_N_ELEMENTS(pages))
        gtk_stack_set_visible_child_name(ui->pages, pages[index]);
    if (adw_flap_get_folded(ui->navigation))
        adw_flap_set_reveal_flap(ui->navigation, FALSE);
    tlGoMapped(ui->handle);
}

static GtkWidget *make_sidebar(TinglanUI *ui)
{
    GtkWidget *sidebar = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    gtk_widget_add_css_class(sidebar, "sidebar");
    gtk_widget_set_size_request(sidebar, 200, -1);
    /* Stop expansion propagated by the track labels from widening the flap. */
    gtk_widget_set_hexpand(sidebar, FALSE);
    GtkWidget *brand = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    pad(brand, 11, 26);
    ui->logo = GTK_IMAGE(gtk_image_new_from_icon_name("audio-x-generic-symbolic"));
    gtk_image_set_pixel_size(ui->logo, 24);
    gtk_box_append(GTK_BOX(brand), GTK_WIDGET(ui->logo));
    gtk_box_append(GTK_BOX(brand), label("听澜", "heading"));
    gtk_box_append(GTK_BOX(sidebar), brand);
    GtkWidget *nav = gtk_list_box_new();
    gtk_widget_add_css_class(nav, "navigation-sidebar");
    pad(nav, 0, 10);
    const char *names[] = {"搜索", "我的收藏", "我的歌单", "播放历史"};
    const char *icons[] = {"system-search-symbolic", "emblem-favorite-symbolic", "view-list-symbolic", "document-open-recent-symbolic"};
    for (unsigned int i = 0; i < G_N_ELEMENTS(names); i++) {
        GtkWidget *line = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 10);
        pad(line, 5, 6);
        gtk_box_append(GTK_BOX(line), gtk_image_new_from_icon_name(icons[i]));
        gtk_box_append(GTK_BOX(line), label(names[i], NULL));
        gtk_list_box_append(GTK_LIST_BOX(nav), line);
    }
    gtk_list_box_select_row(GTK_LIST_BOX(nav), gtk_list_box_get_row_at_index(GTK_LIST_BOX(nav), 1));
    g_signal_connect(nav, "row-activated", G_CALLBACK(choose_page), ui);
    gtk_box_append(GTK_BOX(sidebar), nav);

    GtkWidget *spacer = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    gtk_widget_set_vexpand(spacer, TRUE);
    gtk_box_append(GTK_BOX(sidebar), spacer);
    ui->identity = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 10);
    pad(ui->identity, 20, 20);
    GtkWidget *cover = transport_button(ui, "audio-x-generic-symbolic", "沉浸歌词", LYRICS);
    gtk_widget_add_css_class(cover, "cover-button");
    ui->cover = GTK_IMAGE(gtk_image_new_from_icon_name("audio-x-generic-symbolic"));
    gtk_image_set_pixel_size(ui->cover, 40);
    gtk_button_set_child(GTK_BUTTON(cover), GTK_WIDGET(ui->cover));
    gtk_box_append(GTK_BOX(ui->identity), cover);
    GtkWidget *track = gtk_box_new(GTK_ORIENTATION_VERTICAL, 1);
    gtk_widget_set_valign(track, GTK_ALIGN_CENTER);
    gtk_widget_set_hexpand(track, TRUE);
    ui->title = GTK_LABEL(label("未在播放", "heading"));
    ui->artist = GTK_LABEL(label(NULL, "dim-label"));
    gtk_box_append(GTK_BOX(track), GTK_WIDGET(ui->title));
    gtk_box_append(GTK_BOX(track), GTK_WIDGET(ui->artist));
    gtk_box_append(GTK_BOX(ui->identity), track);
    gtk_box_append(GTK_BOX(sidebar), ui->identity);
    return sidebar;
}

static GtkWidget *make_search(TinglanUI *ui)
{
    GtkWidget *content = gtk_box_new(GTK_ORIENTATION_VERTICAL, 16);
    pad(content, 12, 24);
    gtk_box_append(GTK_BOX(content), label("搜索", "title-1"));
    gtk_widget_set_vexpand(content, TRUE);
    GtkWidget *bar = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 6);
    GtkWidget *entry = gtk_search_entry_new();
    accessible_label(entry, "搜索歌曲或艺人");
    gtk_widget_set_hexpand(entry, TRUE);
    g_signal_connect(entry, "activate", G_CALLBACK(submit_search), ui);
    gtk_box_append(GTK_BOX(bar), entry);
    GtkWidget *button = gtk_button_new_with_label("搜索");
    gtk_widget_add_css_class(button, "suggested-action");
    g_signal_connect_object(button, "clicked", G_CALLBACK(search_clicked), entry, 0);
    gtk_box_append(GTK_BOX(bar), button);
    gtk_box_append(GTK_BOX(content), bar);

    ui->status = GTK_LABEL(label(NULL, "dim-label"));
    gtk_widget_set_visible(GTK_WIDGET(ui->status), FALSE);
    gtk_box_append(GTK_BOX(content), GTK_WIDGET(ui->status));
    ui->rows = gtk_string_list_new(NULL);
    GtkNoSelection *selection = gtk_no_selection_new(G_LIST_MODEL(ui->rows));
    GtkListItemFactory *factory = gtk_signal_list_item_factory_new();
    g_signal_connect(factory, "setup", G_CALLBACK(setup_row), NULL);
    g_signal_connect(factory, "bind", G_CALLBACK(bind_row), NULL);
    GtkWidget *list = gtk_list_view_new(GTK_SELECTION_MODEL(selection), factory);
    gtk_widget_add_css_class(list, "track-list");
    gtk_list_view_set_single_click_activate(GTK_LIST_VIEW(list), TRUE);
    accessible_label(list, "搜索结果");
    g_signal_connect(list, "activate", G_CALLBACK(activate_row), ui);
    GtkWidget *scroller = gtk_scrolled_window_new();
    gtk_widget_set_vexpand(scroller, TRUE);
    gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(scroller), GTK_POLICY_NEVER,
                                   GTK_POLICY_AUTOMATIC);
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(scroller), list);
    ui->scroll = gtk_scrolled_window_get_vadjustment(GTK_SCROLLED_WINDOW(scroller));
    g_signal_connect(ui->scroll, "changed", G_CALLBACK(scroll_changed), ui);
    g_signal_connect(ui->scroll, "value-changed", G_CALLBACK(scroll_changed), ui);
    ui->search_body = GTK_STACK(gtk_stack_new());
    gtk_widget_set_vexpand(GTK_WIDGET(ui->search_body), TRUE);
    ui->empty_search = ADW_STATUS_PAGE(adw_status_page_new());
    adw_status_page_set_icon_name(ui->empty_search, "system-search-symbolic");
    gtk_stack_add_named(ui->search_body, scroller, "results");
    gtk_stack_add_named(ui->search_body, GTK_WIDGET(ui->empty_search), "status");
    gtk_box_append(GTK_BOX(content), GTK_WIDGET(ui->search_body));
    GtkWidget *clamp = adw_clamp_new();
    adw_clamp_set_maximum_size(ADW_CLAMP(clamp), 920);
    adw_clamp_set_child(ADW_CLAMP(clamp), content);
    return clamp;
}

static void history_activate(GtkListView *list, unsigned int pos, TinglanUI *ui)
{
    (void)list;
    tlGoHistoryRow(ui->handle, pos);
}

static GtkWidget *make_history(TinglanUI *ui)
{
    ui->history_page = GTK_STACK(gtk_stack_new());
    gtk_widget_set_vexpand(GTK_WIDGET(ui->history_page), TRUE);
    GtkWidget *empty = adw_status_page_new();
    adw_status_page_set_icon_name(ADW_STATUS_PAGE(empty), "document-open-recent-symbolic");
    adw_status_page_set_title(ADW_STATUS_PAGE(empty), "暂无播放记录");
    gtk_stack_add_named(ui->history_page, empty, "empty");
    ui->history_rows = gtk_string_list_new(NULL);
    GtkNoSelection *selection = gtk_no_selection_new(G_LIST_MODEL(ui->history_rows));
    GtkListItemFactory *factory = gtk_signal_list_item_factory_new();
    g_signal_connect(factory, "setup", G_CALLBACK(setup_row), NULL);
    g_signal_connect(factory, "bind", G_CALLBACK(bind_row), NULL);
    GtkWidget *list = gtk_list_view_new(GTK_SELECTION_MODEL(selection), factory);
    gtk_widget_add_css_class(list, "track-list");
    gtk_list_view_set_single_click_activate(GTK_LIST_VIEW(list), TRUE);
    accessible_label(list, "本机播放历史");
    g_signal_connect(list, "activate", G_CALLBACK(history_activate), ui);
    GtkWidget *scroll = gtk_scrolled_window_new();
    pad(scroll, 12, 16);
    gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(scroll), GTK_POLICY_NEVER, GTK_POLICY_AUTOMATIC);
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(scroll), list);
    gtk_stack_add_named(ui->history_page, scroll, "tracks");
    GtkWidget *content = gtk_box_new(GTK_ORIENTATION_VERTICAL, 16);
    pad(content, 12, 24);
    gtk_box_append(GTK_BOX(content), label("播放历史", "title-1"));
    gtk_box_append(GTK_BOX(content), GTK_WIDGET(ui->history_page));
    GtkWidget *clamp = adw_clamp_new();
    adw_clamp_set_maximum_size(ADW_CLAMP(clamp), 920);
    adw_clamp_set_child(ADW_CLAMP(clamp), content);
    return clamp;
}

static void queue_activate(GtkListView *list, unsigned int pos, TinglanUI *ui)
{
    (void)list;
    tlGoQueueAction(ui->handle, 1, pos);
}

static void clear_queue_response(AdwMessageDialog *dialog, const char *response, TinglanUI *ui)
{
    (void)dialog;
    if (active == ui && g_str_equal(response, "clear"))
        tlGoQueueAction(ui->handle, 3, 0);
}

static void request_queue_clear(GtkButton *button, TinglanUI *ui)
{
    (void)button;
    AdwMessageDialog *dialog = ADW_MESSAGE_DIALOG(adw_message_dialog_new(ui->window, "清空播放队列？", NULL));
    adw_message_dialog_add_response(dialog, "cancel", "取消");
    adw_message_dialog_add_response(dialog, "clear", "清空");
    adw_message_dialog_set_default_response(dialog, "cancel");
    adw_message_dialog_set_close_response(dialog, "cancel");
    adw_message_dialog_set_response_appearance(dialog, "clear", ADW_RESPONSE_DESTRUCTIVE);
    g_signal_connect(dialog, "response", G_CALLBACK(clear_queue_response), ui);
    gtk_window_present(GTK_WINDOW(dialog));
}

static GtkWidget *make_queue(TinglanUI *ui)
{
    GtkWidget *panel = gtk_box_new(GTK_ORIENTATION_VERTICAL, 8);
    gtk_widget_set_size_request(panel, 280, -1);
    gtk_widget_add_css_class(panel, "sidebar");
    GtkWidget *toolbar = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    pad(toolbar, 8, 12);
    GtkWidget *title = label("播放队列", "title-3");
    gtk_widget_set_hexpand(title, TRUE);
    gtk_box_append(GTK_BOX(toolbar), title);
    ui->queue_clear = GTK_BUTTON(gtk_button_new_with_label("清空队列"));
    gtk_widget_set_sensitive(GTK_WIDGET(ui->queue_clear), FALSE);
    g_signal_connect(ui->queue_clear, "clicked", G_CALLBACK(request_queue_clear), ui);
    gtk_box_append(GTK_BOX(toolbar), GTK_WIDGET(ui->queue_clear));
    gtk_box_append(GTK_BOX(panel), toolbar);
    ui->queue_rows = gtk_string_list_new(NULL);
    ui->queue_selection = gtk_single_selection_new(G_LIST_MODEL(ui->queue_rows));
    gtk_single_selection_set_autoselect(ui->queue_selection, FALSE);
    gtk_single_selection_set_can_unselect(ui->queue_selection, TRUE);
    GtkListItemFactory *factory = gtk_signal_list_item_factory_new();
    g_signal_connect(factory, "setup", G_CALLBACK(setup_row), ui);
    g_signal_connect(factory, "bind", G_CALLBACK(bind_row), NULL);
    GtkWidget *list = gtk_list_view_new(GTK_SELECTION_MODEL(ui->queue_selection), factory);
    gtk_widget_add_css_class(list, "track-list");
    gtk_list_view_set_single_click_activate(GTK_LIST_VIEW(list), TRUE);
    accessible_label(list, "播放队列曲目");
    g_signal_connect(list, "activate", G_CALLBACK(queue_activate), ui);
    GtkWidget *scroll = gtk_scrolled_window_new();
    gtk_widget_set_vexpand(scroll, TRUE);
    gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(scroll), GTK_POLICY_NEVER, GTK_POLICY_AUTOMATIC);
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(scroll), list);
    gtk_box_append(GTK_BOX(panel), scroll);
    gtk_widget_set_visible(panel, FALSE);
    return panel;
}

static void toggle_queue(GtkToggleButton *button, TinglanUI *ui)
{
    adw_flap_set_reveal_flap(ui->queue_flap, gtk_toggle_button_get_active(button));
    tlGoMapped(ui->handle);
}

static void choice_activated(GSimpleAction *action, GVariant *value, TinglanUI *ui)
{
    int id = GPOINTER_TO_INT(g_object_get_data(G_OBJECT(action), "tl-action"));
    tlGoAction(ui->handle, id, g_variant_get_int32(value));
}

static GMenu *choice_menu(TinglanUI *ui, const char *name, const char *const *labels,
                          int count, int action_id, GSimpleAction **action_out)
{
    GSimpleAction *action = g_simple_action_new_stateful(name, G_VARIANT_TYPE_INT32, g_variant_new_int32(0));
    g_object_set_data(G_OBJECT(action), "tl-action", GINT_TO_POINTER(action_id));
    g_signal_connect(action, "activate", G_CALLBACK(choice_activated), ui);
    g_action_map_add_action(G_ACTION_MAP(ui->window), G_ACTION(action));
    *action_out = action;
    g_object_unref(action);
    GMenu *model = g_menu_new();
    char *detailed = g_strdup_printf("win.%s", name);
    for (int i = 0; i < count; i++) {
        GMenuItem *item = g_menu_item_new(labels[i], NULL);
        g_menu_item_set_action_and_target(item, detailed, "i", i);
        g_menu_append_item(model, item);
        g_object_unref(item);
    }
    g_free(detailed);
    return model;
}

static GtkWidget *menu_button(const char *icon, const char *title, GMenu *menu)
{
    GtkWidget *button = gtk_menu_button_new();
    gtk_menu_button_set_icon_name(GTK_MENU_BUTTON(button), icon);
    gtk_menu_button_set_menu_model(GTK_MENU_BUTTON(button), G_MENU_MODEL(menu));
    gtk_widget_set_tooltip_text(button, title);
    gtk_widget_add_css_class(button, "flat");
    accessible_label(button, title);
    g_object_unref(menu);
    return button;
}

static void volume_changed(GtkScaleButton *button, double value, TinglanUI *ui)
{
    (void)button;
    if (!ui->volume_sync)
        tlGoAction(ui->handle, VOLUME, value);
}

static GtkWidget *make_transport(TinglanUI *ui)
{
    GtkWidget *surface = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    ui->transport_surface = GTK_BOX(surface);
    gtk_widget_add_css_class(surface, "tinglan-player");
    ui->transport_layout = GTK_CENTER_BOX(gtk_center_box_new());
    pad(GTK_WIDGET(ui->transport_layout), 10, 18);
    gtk_box_append(GTK_BOX(surface), GTK_WIDGET(ui->transport_layout));
    ui->tools = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 3);
    g_object_ref_sink(ui->tools); /* Keep these controls while folded. */
    g_object_set_data_full(G_OBJECT(ui->window), "tl-transport-tools", ui->tools, g_object_unref);
    gtk_widget_set_valign(ui->tools, GTK_ALIGN_CENTER);
    const char *modes[] = {"顺序播放", "列表循环", "单曲循环", "随机播放"};
    const char *qualities[] = {"标准", "无损"};
    ui->mode_menu = GTK_MENU_BUTTON(menu_button("media-playlist-repeat-symbolic", "播放模式",
        choice_menu(ui, "play-mode", modes, G_N_ELEMENTS(modes), PLAY_MODE, &ui->mode_action)));
    gtk_box_append(GTK_BOX(ui->tools), GTK_WIDGET(ui->mode_menu));
    gtk_box_append(GTK_BOX(ui->tools), menu_button("audio-x-generic-symbolic", "优先音质",
        choice_menu(ui, "quality", qualities, G_N_ELEMENTS(qualities), QUALITY, &ui->quality_action)));
    gtk_center_box_set_start_widget(ui->transport_layout, ui->tools);
    GtkWidget *center = ui->center = gtk_box_new(GTK_ORIENTATION_VERTICAL, 4);
    gtk_widget_set_size_request(center, 300, -1);
    gtk_center_box_set_center_widget(ui->transport_layout, center);
    GtkWidget *bar = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 6);
    gtk_widget_set_halign(bar, GTK_ALIGN_CENTER);
    gtk_box_append(GTK_BOX(center), bar);
    gtk_box_append(GTK_BOX(bar), transport_button(ui, "media-skip-backward-symbolic",
                                                   "上一首", PREVIOUS));
    ui->play = GTK_BUTTON(transport_button(ui, "media-playback-start-symbolic",
                                          "播放", TOGGLE_PLAYBACK));
    gtk_widget_remove_css_class(GTK_WIDGET(ui->play), "flat");
    gtk_widget_add_css_class(GTK_WIDGET(ui->play), "suggested-action");
    gtk_widget_add_css_class(GTK_WIDGET(ui->play), "transport-primary");
    gtk_widget_set_valign(GTK_WIDGET(ui->play), GTK_ALIGN_CENTER);
    gtk_box_append(GTK_BOX(bar), GTK_WIDGET(ui->play));
    gtk_box_append(GTK_BOX(bar), transport_button(ui, "media-skip-forward-symbolic",
                                                   "下一首", NEXT));
    GtkWidget *scale = gtk_scale_new_with_range(GTK_ORIENTATION_HORIZONTAL, 0, 1, 1);
    ui->position = GTK_RANGE(scale);
    gtk_scale_set_draw_value(GTK_SCALE(scale), FALSE);
    gtk_widget_set_hexpand(scale, TRUE);
    gtk_widget_set_sensitive(scale, FALSE);
    accessible_label(scale, "播放进度");
    g_signal_connect(scale, "value-changed", G_CALLBACK(position_changed), ui);
    GtkEventController *pointer = gtk_event_controller_legacy_new();
    gtk_event_controller_set_propagation_phase(pointer, GTK_PHASE_CAPTURE);
    g_signal_connect(pointer, "event", G_CALLBACK(seek_event), ui);
    gtk_widget_add_controller(scale, pointer);
    GtkWidget *progress = ui->progress = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 10);
    ui->elapsed = GTK_LABEL(label("0:00", "dim-label"));
    ui->duration = GTK_LABEL(label("0:00", "dim-label"));
    gtk_box_append(GTK_BOX(progress), GTK_WIDGET(ui->elapsed));
    gtk_box_append(GTK_BOX(progress), scale);
    gtk_box_append(GTK_BOX(progress), GTK_WIDGET(ui->duration));
    gtk_box_append(GTK_BOX(center), progress);
    GtkWidget *options = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 4);
    gtk_widget_set_valign(options, GTK_ALIGN_CENTER);
    ui->volume = GTK_SCALE_BUTTON(gtk_volume_button_new());
    gtk_widget_set_tooltip_text(GTK_WIDGET(ui->volume), "音量");
    g_signal_connect(ui->volume, "value-changed", G_CALLBACK(volume_changed), ui);
    gtk_box_append(GTK_BOX(options), GTK_WIDGET(ui->volume));
    GMenu *compact = g_menu_new();
    g_menu_append_submenu(compact, "播放模式", gtk_menu_button_get_menu_model(ui->mode_menu));
    GtkWidget *quality = gtk_widget_get_last_child(ui->tools);
    g_menu_append_submenu(compact, "音质", gtk_menu_button_get_menu_model(GTK_MENU_BUTTON(quality)));
    ui->compact_tools = GTK_MENU_BUTTON(menu_button("view-more-symbolic", "播放选项", compact));
    gtk_widget_set_visible(GTK_WIDGET(ui->compact_tools), FALSE);
    gtk_box_prepend(GTK_BOX(options), GTK_WIDGET(ui->compact_tools));
    GtkWidget *queue = gtk_toggle_button_new();
    ui->queue_toggle = GTK_TOGGLE_BUTTON(queue);
    gtk_button_set_icon_name(GTK_BUTTON(queue), "view-list-symbolic");
    gtk_widget_set_tooltip_text(queue, "播放队列");
    accessible_label(queue, "播放队列");
    g_signal_connect(queue, "toggled", G_CALLBACK(toggle_queue), ui);
    gtk_widget_add_css_class(queue, "flat");
    gtk_box_append(GTK_BOX(options), queue);
    gtk_center_box_set_end_widget(ui->transport_layout, options);
    return surface;
}

static void mapped(GtkWidget *window, TinglanUI *ui)
{
    (void)window;
    if (active == ui)
        tlGoMapped(ui->handle);
}

static void close_ui(TinglanUI *ui)
{
    if (ui->closing)
        return;
    ui->closing = TRUE;
    active = NULL;
    tlGoClosing(ui->handle);
}

static gboolean close_requested(GtkWindow *window, TinglanUI *ui)
{
    (void)window;
    close_ui(ui);
    return FALSE;
}

static void shutdown(GApplication *application, TinglanUI *ui)
{
    (void)application;
    close_ui(ui);
}

static void reveal_queue_changed(AdwFlap *flap, GParamSpec *pspec, TinglanUI *ui)
{
    (void)pspec;
    gtk_toggle_button_set_active(ui->queue_toggle, adw_flap_get_reveal_flap(flap));
}

static void toggle_navigation(GtkButton *button, TinglanUI *ui)
{
    (void)button;
    adw_flap_set_reveal_flap(ui->navigation, !adw_flap_get_reveal_flap(ui->navigation));
}

static void layout_shell(TinglanUI *ui)
{
    gboolean compact = adw_flap_get_folded(ui->navigation);
    gtk_widget_set_visible(ui->nav_toggle, compact && !ui->stage);
    gtk_widget_set_visible(ui->compact_brand, compact || ui->stage);
    gtk_widget_set_visible(ui->stage_back, ui->stage);
    if (ui->stage || !compact)
        adw_flap_set_reveal_flap(ui->navigation, !ui->stage);
    if (ui->compact != compact) {
        g_object_ref(ui->progress);
        gtk_box_remove(ui->compact ? ui->transport_surface : GTK_BOX(ui->center), ui->progress);
        if (compact) {
            pad(ui->progress, 0, 18);
            gtk_widget_set_margin_bottom(ui->progress, 8);
            gtk_box_append(ui->transport_surface, ui->progress);
        } else {
            pad(ui->progress, 0, 0);
            gtk_box_append(GTK_BOX(ui->center), ui->progress);
        }
        g_object_unref(ui->progress);
        gtk_widget_set_size_request(ui->center, compact ? -1 : 300, -1);
        ui->compact = compact;
    }
    GtkWidget *want = compact ? (ui->stage ? NULL : ui->identity) : ui->tools;
    GtkWidget *had = gtk_center_box_get_start_widget(ui->transport_layout);
    if (want != had) {
        if (had == ui->identity)
            g_object_ref(had);
        gtk_center_box_set_start_widget(ui->transport_layout, NULL);
        if (had == ui->identity) {
            pad(had, 20, 20);
            gtk_box_append(GTK_BOX(ui->sidebar), had);
            g_object_unref(had);
        }
        if (want == ui->identity) {
            g_object_ref(want);
            gtk_box_remove(GTK_BOX(ui->sidebar), want);
            pad(want, 0, 0);
        }
        gtk_center_box_set_start_widget(ui->transport_layout, want);
        if (want == ui->identity)
            g_object_unref(want);
    }
    pad(GTK_WIDGET(ui->transport_layout), 10, compact ? 8 : 18);
    gtk_widget_set_visible(GTK_WIDGET(ui->compact_tools), compact);
}

static void fold_changed(AdwFlap *flap, GParamSpec *pspec, TinglanUI *ui)
{
    (void)flap;
    (void)pspec;
    layout_shell(ui);
}

static void preferences_action(GSimpleAction *action, GVariant *parameter, gpointer data)
{
    (void)action;
    (void)parameter;
    TinglanUI *ui = data;
    if (ui->stage)
        tlGoAction(ui->handle, LYRICS, 0);
    gtk_stack_set_visible_child_name(ui->pages, "settings");
    tlGoMapped(ui->handle);
}

static void fullscreen_action(GSimpleAction *action, GVariant *parameter, gpointer data)
{
    TinglanUI *ui = data;
    (void)action;
    (void)parameter;
    if (gtk_window_is_fullscreen(ui->window))
        gtk_window_unfullscreen(ui->window);
    else
        gtk_window_fullscreen(ui->window);
}

static void about_action(GSimpleAction *action, GVariant *parameter, gpointer data)
{
    TinglanUI *ui = data;
    (void)action;
    (void)parameter;
    adw_show_about_window(ui->window, "application-name", "听澜", "license-type", GTK_LICENSE_AGPL_3_0, NULL);
}

static void install_css(const char *collection_css)
{
    static const char css[] =
        ".tinglan-player { background: @view_bg_color; }"
        ".tinglan-player scale { padding: 0; }"
        ".tinglan-player scale trough { min-height: 3px; }"
        "button.transport-primary { min-width: 44px; min-height: 44px; padding: 0; border-width: 0; border-radius: 999px; }"
        "button.cover-button { min-width: 40px; min-height: 40px; padding: 0; border-radius: 6px; background: alpha(currentColor,.08); }";
    GtkCssProvider *provider = gtk_css_provider_new();
    char *combined = g_strconcat(css, "\n", collection_css, NULL);
    gtk_css_provider_load_from_data(provider, combined, -1);
    g_free(combined);
    gtk_style_context_add_provider_for_display(gdk_display_get_default(), GTK_STYLE_PROVIDER(provider), GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
    g_object_unref(provider);
}

static void activate(GApplication *application, TinglanUI *ui)
{
    if (ui->closing)
        return;
    if (ui->window) {
        gtk_window_present(ui->window);
        return;
    }
    ui->window = GTK_WINDOW(adw_application_window_new(GTK_APPLICATION(application)));
    gtk_window_set_title(ui->window, "听澜");
    gtk_window_set_default_size(ui->window, 1140, 760);
    ui->navigation = ADW_FLAP(adw_flap_new());
    GtkWidget *page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    gtk_widget_set_hexpand(page, TRUE);
    GtkWidget *header = adw_header_bar_new();
    ui->page_title = GTK_LABEL(label(NULL, "title"));
    adw_header_bar_set_title_widget(ADW_HEADER_BAR(header), GTK_WIDGET(ui->page_title));
    gtk_widget_add_css_class(header, "flat");
    ui->nav_toggle = gtk_button_new_from_icon_name("sidebar-show-symbolic");
    accessible_label(ui->nav_toggle, "导航");
    g_signal_connect(ui->nav_toggle, "clicked", G_CALLBACK(toggle_navigation), ui);
    ui->stage_back = transport_button(ui, "go-previous-symbolic", "返回", LYRICS);
    ui->compact_brand = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    ui->compact_logo = GTK_IMAGE(gtk_image_new_from_icon_name("audio-x-generic-symbolic"));
    gtk_image_set_pixel_size(ui->compact_logo, 24);
    gtk_box_append(GTK_BOX(ui->compact_brand), GTK_WIDGET(ui->compact_logo));
    gtk_box_append(GTK_BOX(ui->compact_brand), label("听澜", "heading"));
    adw_header_bar_pack_start(ADW_HEADER_BAR(header), ui->nav_toggle);
    adw_header_bar_pack_start(ADW_HEADER_BAR(header), ui->stage_back);
    adw_header_bar_pack_start(ADW_HEADER_BAR(header), ui->compact_brand);
    GMenu *main_menu = g_menu_new();
    GActionEntry entries[] = {{.name = "preferences", .activate = preferences_action},
                              {.name = "fullscreen", .activate = fullscreen_action},
                              {.name = "about", .activate = about_action}};
    g_action_map_add_action_entries(G_ACTION_MAP(ui->window), entries, G_N_ELEMENTS(entries), ui);
    g_menu_append(main_menu, "全屏", "win.fullscreen");
    g_menu_append(main_menu, "设置", "win.preferences");
    const char *fullscreen_keys[] = {"F11", NULL};
    gtk_application_set_accels_for_action(GTK_APPLICATION(application), "win.fullscreen", fullscreen_keys);
    g_menu_append(main_menu, "关于听澜", "win.about");
    adw_header_bar_pack_end(ADW_HEADER_BAR(header), menu_button("open-menu-symbolic", "主菜单", main_menu));
    gtk_box_append(GTK_BOX(page), header);
    ui->queue_flap = ADW_FLAP(adw_flap_new());
    adw_flap_set_flap_position(ui->queue_flap, GTK_PACK_END);
    adw_flap_set_reveal_flap(ui->queue_flap, FALSE);
    adw_flap_set_locked(ui->queue_flap, TRUE);
    ui->pages = GTK_STACK(gtk_stack_new());
    gtk_stack_set_hhomogeneous(ui->pages, FALSE);
    gtk_widget_set_hexpand(GTK_WIDGET(ui->pages), TRUE);
    gtk_widget_set_vexpand(GTK_WIDGET(ui->queue_flap), TRUE);
    gtk_stack_add_named(ui->pages, tl_make_library(ui->handle, ui->window, 0), "favorites");
    gtk_stack_add_named(ui->pages, tl_make_library(ui->handle, ui->window, 1), "playlists");
    gtk_stack_add_named(ui->pages, make_search(ui), "search");
    gtk_stack_add_named(ui->pages, make_history(ui), "history");
    gtk_stack_add_named(ui->pages, tl_make_settings(ui->handle, ui->window), "settings");
    gtk_stack_add_named(ui->pages, tl_make_lyrics(ui->handle, ui->window), "lyrics");
    adw_flap_set_content(ui->queue_flap, GTK_WIDGET(ui->pages));
    ui->queue_panel = make_queue(ui);
    /* The reveal toggle can wake Go before the panel has been mapped. */
    g_signal_connect(ui->queue_panel, "map", G_CALLBACK(mapped), ui);
    gtk_widget_set_visible(ui->queue_panel, TRUE);
    adw_flap_set_flap(ui->queue_flap, ui->queue_panel);
    adw_flap_set_separator(ui->queue_flap, gtk_separator_new(GTK_ORIENTATION_VERTICAL));
    g_signal_connect(ui->queue_flap, "notify::reveal-flap", G_CALLBACK(reveal_queue_changed), ui);
    gtk_box_append(GTK_BOX(page), GTK_WIDGET(ui->queue_flap));
    gtk_box_append(GTK_BOX(page), gtk_separator_new(GTK_ORIENTATION_HORIZONTAL));
    gtk_box_append(GTK_BOX(page), make_transport(ui));
    ui->sidebar = make_sidebar(ui);
    adw_flap_set_flap(ui->navigation, ui->sidebar);
    adw_flap_set_separator(ui->navigation, gtk_separator_new(GTK_ORIENTATION_VERTICAL));
    adw_flap_set_content(ui->navigation, page);
    g_signal_connect(ui->navigation, "notify::folded", G_CALLBACK(fold_changed), ui);
    layout_shell(ui);
    adw_application_window_set_content(ADW_APPLICATION_WINDOW(ui->window), GTK_WIDGET(ui->navigation));
    g_signal_connect(ui->window, "map", G_CALLBACK(mapped), ui);
    g_signal_connect(ui->window, "close-request", G_CALLBACK(close_requested), ui);
    active = ui;
    tlGoReady(ui->handle);
    if (active == ui)
        gtk_window_present(ui->window);
}

int tl_run(uintptr_t handle, const char *collection_css)
{
    if (!gtk_init_check()) {
        g_printerr("听澜: could not initialize GTK (no display)\n");
        return 1;
    }
    install_css(collection_css);
    TinglanUI ui = {.handle = handle};
    /* Profile ownership and cross-frontend single-instance policy live in Go. */
    AdwApplication *application = adw_application_new("org.tinglan.unofficial.gtk",
                                                      G_APPLICATION_NON_UNIQUE);
    g_signal_connect(application, "activate", G_CALLBACK(activate), &ui);
    g_signal_connect(application, "shutdown", G_CALLBACK(shutdown), &ui);
    int status = g_application_run(G_APPLICATION(application), 0, NULL);
    close_ui(&ui);
    g_object_unref(application);
    return status;
}

void tl_present(void)
{
    if (active)
        gtk_window_present(active->window);
}

void tl_hide(void)
{
    if (active)
        gtk_widget_set_visible(GTK_WIDGET(active->window), FALSE);
}

void tl_quit(void)
{
    if (active)
        g_application_quit(G_APPLICATION(gtk_window_get_application(active->window)));
}

int tl_mapped(void)
{
    return active && gtk_widget_get_mapped(GTK_WIDGET(active->window));
}

static void sync_search_body(TinglanUI *ui)
{
    const char *text = gtk_label_get_text(ui->status);
    gboolean has_rows = g_list_model_get_n_items(G_LIST_MODEL(ui->rows)) != 0;
    gboolean centered = !has_rows && *text;
    gtk_widget_set_visible(GTK_WIDGET(ui->status), has_rows && *text);
    if (centered)
        adw_status_page_set_title(ui->empty_search, text);
    gtk_stack_set_visible_child_name(ui->search_body, centered ? "status" : "results");
}

void tl_set_status(const char *text)
{
    if (active) {
        gtk_label_set_text(active->status, text ? text : "");
        sync_search_body(active);
    }
}

void tl_set_playback(const char *title, const char *artist, int playing,
                     double pos, double dur)
{
    TinglanUI *ui = active;
    if (!ui)
        return;
    gtk_label_set_text(ui->title, title && *title ? title : "未在播放");
    gtk_label_set_text(ui->artist, artist ? artist : "");
    const char *icon = playing ? "media-playback-pause-symbolic"
                               : "media-playback-start-symbolic";
    if (g_strcmp0(gtk_button_get_icon_name(ui->play), icon) != 0) {
        const char *text = playing ? "暂停" : "播放";
        gtk_button_set_icon_name(ui->play, icon);
        gtk_widget_set_tooltip_text(GTK_WIDGET(ui->play), text);
        accessible_label(GTK_WIDGET(ui->play), text);
    }
    if (ui->seeking)
        return;
    gboolean seekable = isfinite(dur) && dur > 0;
    ui->syncing = TRUE;
    gtk_widget_set_sensitive(GTK_WIDGET(ui->position), seekable);
    gtk_range_set_range(ui->position, 0, seekable ? dur : 1);
    gtk_range_set_value(ui->position, seekable && isfinite(pos) ? CLAMP(pos, 0, dur) : 0);
    int seconds = isfinite(pos) ? (int)CLAMP(pos, 0, 86400) : 0;
    int total = seekable ? (int)CLAMP(dur, 0, 86400) : 0;
    char text[24];
    g_snprintf(text, sizeof(text), "%d:%02d", seconds / 60, seconds % 60);
    gtk_label_set_text(ui->elapsed, text);
    g_snprintf(text, sizeof(text), "%d:%02d", total / 60, total % 60);
    gtk_label_set_text(ui->duration, text);
    ui->syncing = FALSE;
}

void tl_set_preferences(int mode, int quality, double volume)
{
    if (!active)
        return;
    g_simple_action_set_state(active->mode_action, g_variant_new_int32(mode));
    g_simple_action_set_state(active->quality_action, g_variant_new_int32(quality));
    active->volume_sync = TRUE;
    gtk_scale_button_set_value(active->volume, volume);
    active->volume_sync = FALSE;
}

void tl_set_logo(const void *pixels, size_t length)
{
    if (!active || !length)
        return;
    GBytes *bytes = g_bytes_new(pixels, length);
    GdkTexture *texture = gdk_texture_new_from_bytes(bytes, NULL);
    if (texture) {
        gtk_image_set_from_paintable(active->logo, GDK_PAINTABLE(texture));
        gtk_image_set_from_paintable(active->compact_logo, GDK_PAINTABLE(texture));
        g_object_unref(texture);
    }
    g_bytes_unref(bytes);
}

void tl_set_cover(const void *pixels, size_t length, int width, int height, int stride)
{
    if (!active)
        return;
    if (!length) {
        gtk_image_set_from_icon_name(active->cover, "audio-x-generic-symbolic");
        return;
    }
    GBytes *bytes = g_bytes_new(pixels, length);
    GdkTexture *texture = gdk_memory_texture_new(width, height, GDK_MEMORY_B8G8R8A8_PREMULTIPLIED, bytes, stride);
    gtk_image_set_from_paintable(active->cover, GDK_PAINTABLE(texture));
    g_object_unref(texture);
    g_bytes_unref(bytes);
}

void tl_show_lyrics(int visible)
{
    if (!active)
        return;
    TinglanUI *ui = active;
    if (visible && !ui->stage) {
        ui->before_lyrics = gtk_stack_get_visible_child_name(ui->pages);
        gtk_stack_set_visible_child_name(ui->pages, "lyrics");
    } else if (!visible && ui->stage) {
        gtk_stack_set_visible_child_name(ui->pages, ui->before_lyrics ? ui->before_lyrics : "favorites");
    }
    ui->stage = visible != 0;
    layout_shell(ui);
}

void tl_set_rows(unsigned int start, unsigned int remove, const char *const *rows)
{
    TinglanUI *ui = active;
    if (!ui)
        return;
    unsigned int count = g_list_model_get_n_items(G_LIST_MODEL(ui->rows));
    if (start > count)
        return;
    ui->ignore_scroll++;
    gtk_string_list_splice(ui->rows, start, MIN(remove, count - start), rows);
    sync_search_body(ui);
    ui->ignore_scroll--;
}

int tl_history_visible(void)
{
    return active && gtk_widget_get_mapped(GTK_WIDGET(active->history_page));
}

void tl_set_history_rows(unsigned int remove, const char *const *rows)
{
    if (active) {
        gtk_string_list_splice(active->history_rows, 0, remove, rows);
        gboolean has_rows = g_list_model_get_n_items(G_LIST_MODEL(active->history_rows)) != 0;
        gtk_stack_set_visible_child_name(active->history_page, has_rows ? "tracks" : "empty");
    }
}

int tl_queue_visible(void)
{
    return active && adw_flap_get_reveal_flap(active->queue_flap) && gtk_widget_get_mapped(active->queue_panel);
}

void tl_set_queue_rows(unsigned int start, unsigned int remove, const char *const *rows)
{
    if (active)
        gtk_string_list_splice(active->queue_rows, start, remove, rows);
}

void tl_set_queue_index(int index)
{
    if (active) {
        gtk_single_selection_set_selected(active->queue_selection,
            index < 0 ? GTK_INVALID_LIST_POSITION : (unsigned int)index);
        gtk_widget_set_sensitive(GTK_WIDGET(active->queue_clear), index >= 0);
    }
}

void tl_reset_scroll(void)
{
    TinglanUI *ui = active;
    if (!ui)
        return;
    ui->ignore_scroll++;
    gtk_adjustment_set_value(ui->scroll, gtk_adjustment_get_lower(ui->scroll));
    ui->ignore_scroll--;
}
