//go:build linux && gtk && cgo

#include <adwaita.h>
#include <stdint.h>

/* Shared with the cgo bridge. Page IDs 0/1 are the shell's library pages. */
enum { FAVORITES, PLAYLISTS, DETAIL, REMOTE, LIBRARY_PAGE_COUNT };
enum { PLAY = 1, PLAY_ALL, OPEN, BACK, FAVORITE, ADD, REMOVE, CREATE, RENAME,
       DELETE, IMPORT_FAVORITES, BROWSE_REMOTE, IMPORT_PLAYLIST, UPDATE };
enum { CAN_WRITE = 1, CAN_IMPORT = 2, CAN_EDIT = 4, CAN_UPDATE = 8 };

extern uintptr_t tlGoLibraryNew(uintptr_t session);
extern void tlGoLibraryFree(uintptr_t handle);
extern void tlGoLibraryStop(uintptr_t handle);
extern void tlGoLibraryAction(uintptr_t handle, int page, int action, unsigned int row);
extern void tlGoLibraryRespond(uintptr_t handle, char *text, unsigned int selected);

typedef struct LibraryState LibraryState;
typedef struct {
    LibraryState *state;
    int id;
    GtkWidget *root, *list, *play, *create, *import, *rename, *remove, *update;
    GtkLabel *title, *count, *status, *description;
    GtkStringList *rows;
} LibraryPage;

struct LibraryState {
    uintptr_t session, handle;
    GtkWindow *window;
    GtkStack *playlist_stack;
    LibraryPage pages[LIBRARY_PAGE_COUNT];
};

/* The single-window GTK frontend renders on the GTK thread. The window owns
 * this state and clears the lookup before freeing the cgo handle. */
static LibraryState *library;

static void free_library(gpointer data)
{
    LibraryState *state = data;
    if (library == state)
        library = NULL;
    tlGoLibraryFree(state->handle);
    g_free(state);
}

static GtkWidget *text(const char *value, const char *style)
{
    GtkWidget *w = gtk_label_new(value);
    gtk_label_set_xalign(GTK_LABEL(w), 0);
    gtk_label_set_ellipsize(GTK_LABEL(w), PANGO_ELLIPSIZE_END);
    if (style)
        gtk_widget_add_css_class(w, style);
    return w;
}

static void clicked(GtkButton *button, LibraryPage *page)
{
    tlGoLibraryAction(page->state->handle, page->id,
                      GPOINTER_TO_INT(g_object_get_data(G_OBJECT(button), "action")), 0);
}

static GtkWidget *action_button(LibraryPage *page, GtkBox *bar, const char *name,
                                int action, const char *style)
{
    GtkWidget *w = gtk_button_new_with_label(name);
    if (style)
        gtk_widget_add_css_class(w, style);
    g_object_set_data(G_OBJECT(w), "action", GINT_TO_POINTER(action));
    g_signal_connect(w, "clicked", G_CALLBACK(clicked), page);
    gtk_box_append(bar, w);
    return w;
}

static void row_action(GtkButton *button, GtkListItem *item)
{
    LibraryPage *page = g_object_get_data(G_OBJECT(item), "page");
    unsigned int pos = gtk_list_item_get_position(item);
    if (pos != GTK_INVALID_LIST_POSITION)
        tlGoLibraryAction(page->state->handle, page->id,
                          GPOINTER_TO_INT(g_object_get_data(G_OBJECT(button), "action")), pos);
}

static GtkWidget *row_button(GtkListItem *item, const char *icon, const char *name, int action)
{
    GtkWidget *w = gtk_button_new_from_icon_name(icon);
    gtk_widget_add_css_class(w, "flat");
    gtk_widget_set_tooltip_text(w, name);
    gtk_accessible_update_property(GTK_ACCESSIBLE(w), GTK_ACCESSIBLE_PROPERTY_LABEL, name, -1);
    gtk_widget_set_valign(w, GTK_ALIGN_CENTER);
    g_object_set_data(G_OBJECT(w), "action", GINT_TO_POINTER(action));
    g_signal_connect_object(w, "clicked", G_CALLBACK(row_action), item, 0);
    return w;
}

static void setup_row(GtkSignalListItemFactory *factory, GtkListItem *item, LibraryPage *page)
{
    (void)factory;
    gboolean card = page->id == PLAYLISTS || page->id == REMOTE;
    GtkWidget *box = gtk_box_new(card ? GTK_ORIENTATION_VERTICAL : GTK_ORIENTATION_HORIZONTAL, card ? 3 : 10);
    if (card) {
        gtk_widget_set_halign(box, GTK_ALIGN_START);
        gtk_widget_add_css_class(box, "playlist-card");
    } else {
        gtk_widget_set_margin_top(box, 8);
        gtk_widget_set_margin_bottom(box, 8);
        gtk_widget_set_margin_start(box, 6);
        gtk_widget_set_margin_end(box, 6);
    }
    GtkWidget *art = gtk_image_new_from_icon_name(card ? "view-list-symbolic" : "audio-x-generic-symbolic");
    gtk_widget_add_css_class(art, "dim-label");
    gtk_image_set_pixel_size(GTK_IMAGE(art), card ? 48 : 24);
    gtk_widget_set_size_request(art, card ? 140 : 40, card ? 140 : 40);
    if (card) {
        gtk_widget_add_css_class(art, "playlist-art");
        gtk_widget_set_halign(art, GTK_ALIGN_START);
        gtk_widget_set_margin_bottom(art, 6);
    }
    gtk_box_append(GTK_BOX(box), art);
    GtkWidget *names = gtk_box_new(GTK_ORIENTATION_VERTICAL, 2);
    gtk_widget_set_valign(names, GTK_ALIGN_CENTER);
    gtk_widget_set_hexpand(names, TRUE);
    GtkWidget *title = text(NULL, "row-title");
    GtkWidget *meta = text(NULL, "dim-label");
    if (card) {
        gtk_label_set_max_width_chars(GTK_LABEL(title), 12);
        gtk_label_set_max_width_chars(GTK_LABEL(meta), 12);
        gtk_widget_add_css_class(meta, "playlist-meta");
    }
    gtk_box_append(GTK_BOX(names), title);
    gtk_box_append(GTK_BOX(names), meta);
    gtk_box_append(GTK_BOX(box), names);
    g_object_set_data(G_OBJECT(item), "title", title);
    g_object_set_data(G_OBJECT(item), "meta", meta);
    g_object_set_data(G_OBJECT(item), "page", page);
    if (!card) {
        GtkWidget *duration = text(NULL, "dim-label");
        gtk_widget_add_css_class(duration, "duration");
        gtk_box_append(GTK_BOX(box), duration);
        g_object_set_data(G_OBJECT(item), "duration", duration);
        GtkWidget *star = row_button(item, "non-starred-symbolic", "加入收藏", FAVORITE);
        GtkWidget *add = row_button(item, "list-add-symbolic", "添加到歌单", ADD);
        GtkWidget *remove = row_button(item, "list-remove-symbolic", "从此歌单移除", REMOVE);
        gtk_box_append(GTK_BOX(box), star);
        gtk_box_append(GTK_BOX(box), add);
        gtk_box_append(GTK_BOX(box), remove);
        g_object_set_data(G_OBJECT(item), "star", star);
        g_object_set_data(G_OBJECT(item), "add", add);
        g_object_set_data(G_OBJECT(item), "remove", remove);
    }
    gtk_list_item_set_child(item, box);
}

static void bind_row(GtkSignalListItemFactory *factory, GtkListItem *item, LibraryPage *page)
{
    (void)factory;
    (void)page;
    const char *record = gtk_string_object_get_string(gtk_list_item_get_item(item));
    char **parts = g_strsplit(record, "\n", 4);
    gtk_label_set_text(g_object_get_data(G_OBJECT(item), "title"), parts[0]);
    gtk_label_set_text(g_object_get_data(G_OBJECT(item), "meta"), parts[1]);
    GtkButton *star = g_object_get_data(G_OBJECT(item), "star");
    if (star) {
        gtk_label_set_text(g_object_get_data(G_OBJECT(item), "duration"), parts[2]);
        int flags = atoi(parts[3]);
        const char *name = (flags & 1) ? "取消收藏" : "加入收藏";
        gtk_button_set_icon_name(star, (flags & 1) ? "starred-symbolic" : "non-starred-symbolic");
        gtk_widget_set_tooltip_text(GTK_WIDGET(star), name);
        gtk_accessible_update_property(GTK_ACCESSIBLE(star), GTK_ACCESSIBLE_PROPERTY_LABEL, name, -1);
        gtk_widget_set_visible(GTK_WIDGET(star), (flags & 2) != 0);
        gtk_widget_set_visible(g_object_get_data(G_OBJECT(item), "add"), (flags & 2) != 0);
        gtk_widget_set_visible(g_object_get_data(G_OBJECT(item), "remove"), (flags & 4) != 0);
    }
    g_strfreev(parts);
}

static void activate_row(GtkWidget *widget, unsigned int pos, LibraryPage *page)
{
    (void)widget;
    int action = page->id == PLAYLISTS ? OPEN : page->id == REMOTE ? IMPORT_PLAYLIST : PLAY;
    tlGoLibraryAction(page->state->handle, page->id, action, pos);
}

static GtkWidget *make_page(LibraryState *state, int id)
{
    LibraryPage *p = &state->pages[id];
    p->state = state;
    p->id = id;
    GtkWidget *content = gtk_box_new(GTK_ORIENTATION_VERTICAL, 12);
    gtk_widget_set_margin_start(content, 24);
    gtk_widget_set_margin_end(content, 24);
    gtk_widget_set_margin_top(content, 12);
    gtk_widget_set_margin_bottom(content, 10);
    GtkWidget *heading = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 12);
    GtkWidget *toolbar = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    if (id == DETAIL || id == REMOTE)
        action_button(p, GTK_BOX(heading), "返回歌单", BACK, "flat");
    const char *titles[] = {"我的收藏", "我的歌单", "歌单", "导入歌单"};
    p->title = GTK_LABEL(text(titles[id], "title-1"));
    p->count = GTK_LABEL(text(NULL, "dim-label"));
    gtk_box_append(GTK_BOX(heading), GTK_WIDGET(p->title));
    gtk_box_append(GTK_BOX(heading), GTK_WIDGET(p->count));
    gtk_box_append(GTK_BOX(content), heading);
    if (id == FAVORITES || id == DETAIL)
        p->play = action_button(p, GTK_BOX(toolbar), "播放全部", PLAY_ALL, "suggested-action");
    if (id == FAVORITES)
        p->import = action_button(p, GTK_BOX(toolbar), "导入收藏", IMPORT_FAVORITES, NULL);
    if (id == PLAYLISTS) {
        p->create = action_button(p, GTK_BOX(toolbar), "新建歌单", CREATE, "suggested-action");
        p->import = action_button(p, GTK_BOX(toolbar), "导入歌单", BROWSE_REMOTE, NULL);
    }
    if (id == DETAIL) {
        p->rename = action_button(p, GTK_BOX(toolbar), "重命名", RENAME, NULL);
        p->remove = action_button(p, GTK_BOX(toolbar), "删除歌单", DELETE, NULL);
        p->update = action_button(p, GTK_BOX(toolbar), "更新本地副本", UPDATE, NULL);
        p->description = GTK_LABEL(text(NULL, "dim-label"));
        gtk_box_append(GTK_BOX(content), GTK_WIDGET(p->description));
    }
    if (id != REMOTE)
        gtk_box_append(GTK_BOX(content), toolbar);
    else
        g_object_unref(g_object_ref_sink(toolbar));
    p->status = GTK_LABEL(text(NULL, "dim-label"));
    gtk_label_set_wrap(p->status, TRUE);
    gtk_box_append(GTK_BOX(content), GTK_WIDGET(p->status));

    GtkListItemFactory *factory = gtk_signal_list_item_factory_new();
    g_signal_connect(factory, "setup", G_CALLBACK(setup_row), p);
    g_signal_connect(factory, "bind", G_CALLBACK(bind_row), p);
    p->rows = gtk_string_list_new(NULL);
    GtkSelectionModel *rows = GTK_SELECTION_MODEL(gtk_no_selection_new(G_LIST_MODEL(p->rows)));
    if (id == PLAYLISTS || id == REMOTE) {
        p->list = gtk_grid_view_new(rows, factory);
        gtk_widget_add_css_class(p->list, "playlist-grid");
        gtk_grid_view_set_min_columns(GTK_GRID_VIEW(p->list), 1);
        gtk_grid_view_set_max_columns(GTK_GRID_VIEW(p->list), 3);
        gtk_grid_view_set_single_click_activate(GTK_GRID_VIEW(p->list), TRUE);
    } else {
        p->list = gtk_list_view_new(rows, factory);
        gtk_list_view_set_single_click_activate(GTK_LIST_VIEW(p->list), TRUE);
        gtk_widget_add_css_class(p->list, "track-list");
    }
    g_signal_connect(p->list, "activate", G_CALLBACK(activate_row), p);
    GtkWidget *scroll = gtk_scrolled_window_new();
    gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(scroll), GTK_POLICY_NEVER, GTK_POLICY_AUTOMATIC);
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(scroll), p->list);
    gtk_widget_set_vexpand(scroll, TRUE);
    gtk_box_append(GTK_BOX(content), scroll);
    p->root = adw_clamp_new();
    adw_clamp_set_maximum_size(ADW_CLAMP(p->root), 920);
    adw_clamp_set_child(ADW_CLAMP(p->root), content);
    return p->root;
}

static gboolean stop_on_close(GtkWindow *window, LibraryState *state)
{
    (void)window;
    tlGoLibraryStop(state->handle);
    return FALSE;
}

GtkWidget *tl_make_library(uintptr_t session, GtkWindow *window, int page)
{
    LibraryState *state = g_object_get_data(G_OBJECT(window), "tinglan-library");
    if (!state) {
        state = g_new0(LibraryState, 1);
        state->session = session;
        state->handle = tlGoLibraryNew(session);
        state->window = window;
        g_object_set_data_full(G_OBJECT(window), "tinglan-library", state, free_library);
        g_signal_connect(window, "close-request", G_CALLBACK(stop_on_close), state);
        library = state;
    }
    if (page == FAVORITES)
        return make_page(state, FAVORITES);
    state->playlist_stack = GTK_STACK(gtk_stack_new());
    gtk_stack_set_hhomogeneous(state->playlist_stack, FALSE);
    gtk_stack_add_named(state->playlist_stack, make_page(state, PLAYLISTS), "playlists");
    gtk_stack_add_named(state->playlist_stack, make_page(state, DETAIL), "detail");
    gtk_stack_add_named(state->playlist_stack, make_page(state, REMOTE), "remote");
    return GTK_WIDGET(state->playlist_stack);
}

uintptr_t tl_library_controller(uintptr_t session)
{
    return library && session == library->session ? library->handle : 0;
}

void tl_library_show(int page)
{
    if (library && library->playlist_stack && page >= PLAYLISTS && page <= REMOTE)
        gtk_stack_set_visible_child(library->playlist_stack, library->pages[page].root);
}

void tl_library_rows(int id, unsigned int start, unsigned int remove, const char *const *rows)
{
    if (library && library->pages[id].rows)
        gtk_string_list_splice(library->pages[id].rows, start, remove, rows);
}

void tl_library_heading(int id, const char *title, const char *count,
                        const char *description, const char *status, unsigned int flags)
{
    if (!library || !library->pages[id].root)
        return;
    LibraryPage *p = &library->pages[id];
    if (*title)
        gtk_label_set_text(p->title, title);
    gtk_label_set_text(p->count, count);
    gtk_label_set_text(p->status, status);
    gtk_widget_set_visible(GTK_WIDGET(p->status), *status != '\0');
    if (p->description) {
        gtk_label_set_text(p->description, description);
        gtk_widget_set_visible(GTK_WIDGET(p->description), *description != '\0');
    }
    if (p->play)
        gtk_widget_set_sensitive(p->play, g_list_model_get_n_items(G_LIST_MODEL(p->rows)) > 0);
    if (p->create)
        gtk_widget_set_visible(p->create, (flags & CAN_WRITE) != 0);
    if (p->import)
        gtk_widget_set_sensitive(p->import, (flags & CAN_IMPORT) != 0);
    if (p->rename)
        gtk_widget_set_visible(p->rename, (flags & CAN_EDIT) != 0);
    if (p->remove)
        gtk_widget_set_visible(p->remove, (flags & CAN_EDIT) != 0);
    if (p->update)
        gtk_widget_set_visible(p->update, (flags & CAN_UPDATE) != 0);
}

static void respond(AdwMessageDialog *dialog, const char *response, gpointer data)
{
    if (g_str_equal(response, "ok")) {
        GtkWidget *field = adw_message_dialog_get_extra_child(dialog);
        const char *text = GTK_IS_ENTRY(field) ? gtk_editable_get_text(GTK_EDITABLE(field)) : "";
        unsigned int selected = GTK_IS_DROP_DOWN(field) ? gtk_drop_down_get_selected(GTK_DROP_DOWN(field)) : GTK_INVALID_LIST_POSITION;
        tlGoLibraryRespond((uintptr_t)data, (char *)text, selected);
    }
}

static void free_response(gpointer data)
{
    tlGoLibraryFree((uintptr_t)data);
}

void tl_library_dialog(uintptr_t callback, const char *title, const char *body,
                       const char *affirmative, const char *entry, const char *const *choices,
                       int destructive)
{
    if (!library) {
        tlGoLibraryFree(callback);
        return;
    }
    AdwMessageDialog *dialog = ADW_MESSAGE_DIALOG(adw_message_dialog_new(library->window, title, body));
    gtk_window_set_destroy_with_parent(GTK_WINDOW(dialog), TRUE);
    g_object_set_data_full(G_OBJECT(dialog), "tinglan-response", (gpointer)callback, free_response);
    adw_message_dialog_add_response(dialog, "cancel", *affirmative ? "取消" : "关闭");
    adw_message_dialog_set_close_response(dialog, "cancel");
    if (*affirmative) {
        adw_message_dialog_add_response(dialog, "ok", affirmative);
        adw_message_dialog_set_response_appearance(dialog, "ok", destructive ? ADW_RESPONSE_DESTRUCTIVE : ADW_RESPONSE_SUGGESTED);
        adw_message_dialog_set_default_response(dialog, destructive ? "cancel" : "ok");
    }
    if (entry) {
        GtkWidget *widget = gtk_entry_new();
        gtk_editable_set_text(GTK_EDITABLE(widget), entry);
        gtk_entry_set_placeholder_text(GTK_ENTRY(widget), "歌单名称");
        gtk_entry_set_activates_default(GTK_ENTRY(widget), TRUE);
        adw_message_dialog_set_extra_child(dialog, widget);
        gtk_window_set_focus(GTK_WINDOW(dialog), widget);
    } else if (choices) {
        adw_message_dialog_set_extra_child(dialog, gtk_drop_down_new_from_strings(choices));
    }
    g_signal_connect(dialog, "response", G_CALLBACK(respond), (gpointer)callback);
    gtk_window_present(GTK_WINDOW(dialog));
}
