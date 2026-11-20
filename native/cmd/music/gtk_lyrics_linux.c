//go:build linux && gtk && cgo

#include <adwaita.h>
#include <stdint.h>

extern uintptr_t tlGoLyricsNew(uintptr_t session);
extern void tlGoLyricsFree(uintptr_t state);
extern void tlGoLyricsMapped(uintptr_t state);
extern void tlGoLyricsOffset(uintptr_t state, int direction);
extern int tlGoLyricsBeginImport(uintptr_t state);
extern void tlGoLyricsFinishImport(uintptr_t state, char *path);

typedef struct {
    GtkWidget parent;
    GtkWidget *artwork, *cover, *words;
} TlLyricSpread;

typedef GtkWidgetClass TlLyricSpreadClass;
G_DEFINE_TYPE(TlLyricSpread, tl_lyric_spread, GTK_TYPE_WIDGET)

static void spread_measure(GtkWidget *widget, GtkOrientation orientation, int for_size,
                           int *minimum, int *natural, int *min_baseline, int *nat_baseline)
{
    (void)widget;
    (void)for_size;
    *minimum = 260;
    *natural = orientation == GTK_ORIENTATION_HORIZONTAL ? 900 : 540;
    *min_baseline = *nat_baseline = -1;
}

static void allocate_at(GtkWidget *widget, int width, int height, int x, int y)
{
    graphene_point_t origin = GRAPHENE_POINT_INIT(x, y);
    gtk_widget_allocate(widget, width, height, -1, gsk_transform_translate(NULL, &origin));
}

/* The experimental frontend still targets libadwaita 1.2, before
 * AdwBreakpointBin. Size the two cells from the available stage allocation. */
static void spread_allocate(GtkWidget *widget, int width, int height, int baseline)
{
    (void)baseline;
    TlLyricSpread *spread = (TlLyricSpread *)widget;
    gboolean compact = width <= 740;
    int inset = compact ? 12 : 24;
    int gap = compact ? 18 : 54;
    int body_width = MIN(width - inset * 2, 1700);
    int start = (width - body_width) / 2;
    int column = compact ? body_width : (body_width - gap) / 2;
    int preferred_cover = compact ? 136 : width >= 1340 && height >= 580 ? 424 : 296;
    int metadata_height = 0;
    gtk_widget_measure(spread->artwork, GTK_ORIENTATION_VERTICAL, column,
                       NULL, &metadata_height, NULL, NULL);
    int cover = MIN(preferred_cover, MIN(column, compact ? height / 3 : height - metadata_height - 12));
    cover = MAX(1, cover);
    int artwork_height = cover + 12 + metadata_height;
    int art_y = compact ? 0 : MAX(0, (height - artwork_height) / 2);
    allocate_at(spread->cover, cover, cover, start + (column - cover) / 2, art_y);
    allocate_at(spread->artwork, column, metadata_height, start, art_y + cover + 12);
    if (compact) {
        int words_y = MIN(artwork_height + gap, height);
        allocate_at(spread->words, column, height - words_y, start, words_y);
    } else {
        allocate_at(spread->words, column, height, start + column + gap, 0);
    }
}

static void spread_dispose(GObject *object)
{
    TlLyricSpread *spread = (TlLyricSpread *)object;
    g_clear_pointer(&spread->artwork, gtk_widget_unparent);
    g_clear_pointer(&spread->cover, gtk_widget_unparent);
    g_clear_pointer(&spread->words, gtk_widget_unparent);
    G_OBJECT_CLASS(tl_lyric_spread_parent_class)->dispose(object);
}

static void spread_snapshot(GtkWidget *widget, GtkSnapshot *snapshot)
{
    TlLyricSpread *spread = (TlLyricSpread *)widget;
    gtk_widget_snapshot_child(widget, spread->cover, snapshot);
    gtk_widget_snapshot_child(widget, spread->artwork, snapshot);
    gtk_widget_snapshot_child(widget, spread->words, snapshot);
}

static void tl_lyric_spread_class_init(TlLyricSpreadClass *klass)
{
    G_OBJECT_CLASS(klass)->dispose = spread_dispose;
    klass->measure = spread_measure;
    klass->size_allocate = spread_allocate;
    klass->snapshot = spread_snapshot;
}

static void tl_lyric_spread_init(TlLyricSpread *spread) { (void)spread; }

typedef struct {
    uintptr_t state;
    GtkWidget *root, *content, *empty, *rows, *scroll, *words;
    GtkWidget *picture, *placeholder, *title, *artist, *offset, *import, *feedback;
    GtkWindow *window;
    GtkNativeDialog *chooser;
    GtkCssProvider *css;
    GdkDisplay *display;
    GPtrArray *items;
    int active;
    guint follow_tick, follow_idle;
} TlLyrics;

/* Like the shell there is one live widget tree, accessed on the GTK thread. */
static TlLyrics *lyrics_view;

static GtkWidget *lyric_label(const char *text, const char *css, gboolean wrap)
{
    GtkWidget *label = gtk_label_new(text);
    if (css)
        gtk_widget_add_css_class(label, css);
    if (wrap) {
        gtk_label_set_wrap(GTK_LABEL(label), TRUE);
        gtk_label_set_wrap_mode(GTK_LABEL(label), PANGO_WRAP_WORD_CHAR);
        gtk_label_set_justify(GTK_LABEL(label), GTK_JUSTIFY_CENTER);
    } else {
        gtk_label_set_ellipsize(GTK_LABEL(label), PANGO_ELLIPSIZE_END);
    }
    return label;
}

static gboolean follow_idle(gpointer data)
{
    TlLyrics *view = data;
    view->follow_idle = 0;
    if (!gtk_widget_get_mapped(view->scroll) || view->active < 0)
        return G_SOURCE_REMOVE;
    GtkWidget *row = g_ptr_array_index(view->items, view->active);
    GtkWidget *viewport = gtk_scrolled_window_get_child(GTK_SCROLLED_WINDOW(view->scroll));
    graphene_rect_t bounds;
    if (gtk_widget_compute_bounds(row, viewport, &bounds)) {
        GtkAdjustment *adj = gtk_scrolled_window_get_vadjustment(GTK_SCROLLED_WINDOW(view->scroll));
        double center = bounds.origin.y + bounds.size.height / 2;
        gtk_adjustment_set_value(adj, gtk_adjustment_get_value(adj) + center - gtk_adjustment_get_page_size(adj) / 2);
    }
    return G_SOURCE_REMOVE;
}

static gboolean follow_tick(GtkWidget *widget, GdkFrameClock *clock, gpointer data)
{
    (void)widget;
    (void)clock;
    TlLyrics *view = data;
    view->follow_tick = 0;
    /* The tick comes before layout. Read viewport bounds after allocation. */
    if (view->follow_idle == 0)
        view->follow_idle = g_idle_add(follow_idle, view);
    return G_SOURCE_REMOVE;
}

static void schedule_follow(TlLyrics *view)
{
    if (view->active >= 0 && view->follow_tick == 0)
        view->follow_tick = gtk_widget_add_tick_callback(view->scroll, follow_tick, view, NULL);
}

static void viewport_changed(GtkAdjustment *adjustment, TlLyrics *view)
{
    (void)adjustment;
    schedule_follow(view);
}

static void offset_clicked(GtkButton *button, TlLyrics *view)
{
    int direction = GPOINTER_TO_INT(g_object_get_data(G_OBJECT(button), "tl-lyric-direction"));
    tlGoLyricsOffset(view->state, direction);
}

static GtkWidget *offset_button(TlLyrics *view, const char *label, const char *help, int direction)
{
    GtkWidget *button = gtk_button_new_with_label(label);
    gtk_widget_set_tooltip_text(button, help);
    gtk_accessible_update_property(GTK_ACCESSIBLE(button), GTK_ACCESSIBLE_PROPERTY_LABEL, help, -1);
    g_object_set_data(G_OBJECT(button), "tl-lyric-direction", GINT_TO_POINTER(direction));
    g_signal_connect(button, "clicked", G_CALLBACK(offset_clicked), view);
    return button;
}

static void import_response(GtkNativeDialog *dialog, int response, TlLyrics *view)
{
    if (response == GTK_RESPONSE_ACCEPT) {
        GFile *file = gtk_file_chooser_get_file(GTK_FILE_CHOOSER(dialog));
        char *path = file ? g_file_get_path(file) : NULL;
        tlGoLyricsFinishImport(view->state, path);
        g_free(path);
        g_clear_object(&file);
    }
    g_clear_object(&view->chooser);
}

static void import_clicked(GtkButton *button, TlLyrics *view)
{
    (void)button;
    if (view->chooser || !tlGoLyricsBeginImport(view->state))
        return;
    view->chooser = GTK_NATIVE_DIALOG(gtk_file_chooser_native_new("导入歌词", view->window,
                                        GTK_FILE_CHOOSER_ACTION_OPEN, "导入", "取消"));
    gtk_native_dialog_set_modal(view->chooser, TRUE);
    GtkFileFilter *filter = gtk_file_filter_new();
    gtk_file_filter_set_name(filter, "歌词 (.lrc, .ttml, .txt)");
    gtk_file_filter_add_suffix(filter, "lrc");
    gtk_file_filter_add_suffix(filter, "ttml");
    gtk_file_filter_add_suffix(filter, "txt");
    gtk_file_chooser_add_filter(GTK_FILE_CHOOSER(view->chooser), filter);
    g_object_unref(filter);
    g_signal_connect(view->chooser, "response", G_CALLBACK(import_response), view);
    gtk_native_dialog_show(view->chooser);
}

static void lyrics_mapped(GtkWidget *root, TlLyrics *view)
{
    (void)root;
    schedule_follow(view);
    /* The stage may map without a transport change (e.g. while paused). */
    extern void tlGoLyricsMapped(uintptr_t state);
    tlGoLyricsMapped(view->state);
}

static void lyrics_unmapped(GtkWidget *root, TlLyrics *view)
{
    (void)root;
    g_clear_handle_id(&view->follow_idle, g_source_remove);
    if (view->follow_tick != 0) {
        gtk_widget_remove_tick_callback(view->scroll, view->follow_tick);
        view->follow_tick = 0;
    }
    if (view->chooser) {
        g_signal_handlers_disconnect_by_data(view->chooser, view);
        gtk_native_dialog_destroy(view->chooser);
        g_clear_object(&view->chooser);
    }
}

static void free_lyrics(gpointer data)
{
    TlLyrics *view = data;
    if (lyrics_view == view)
        lyrics_view = NULL;
    if (view->chooser) {
        g_signal_handlers_disconnect_by_data(view->chooser, view);
        gtk_native_dialog_destroy(view->chooser);
        g_clear_object(&view->chooser);
    }
    g_clear_handle_id(&view->follow_idle, g_source_remove);
    gtk_style_context_remove_provider_for_display(view->display, GTK_STYLE_PROVIDER(view->css));
    g_object_unref(view->display);
    g_object_unref(view->css);
    g_ptr_array_unref(view->items);
    tlGoLyricsFree(view->state);
    g_free(view);
}

GtkWidget *tl_make_lyrics(uintptr_t handle, GtkWindow *window)
{
    TlLyrics *view = g_new0(TlLyrics, 1);
    lyrics_view = view;
    view->state = tlGoLyricsNew(handle);
    view->window = window;
    view->items = g_ptr_array_new();
    view->active = -1;
    view->css = gtk_css_provider_new();
    gtk_css_provider_load_from_data(view->css,
        ".tl-stage-lyric { font-size: 1.35em; }"
        ".tl-stage-active .tl-stage-lyric { font-weight: bold; }"
        ".tl-stage-cover { border-radius: 16px; background-color: alpha(currentColor, 0.08); }", -1);
    view->display = g_object_ref(gtk_widget_get_display(GTK_WIDGET(window)));
    gtk_style_context_add_provider_for_display(view->display, GTK_STYLE_PROVIDER(view->css),
                                               GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);

    view->root = gtk_stack_new();
    gtk_stack_set_hhomogeneous(GTK_STACK(view->root), FALSE);
    gtk_stack_set_vhomogeneous(GTK_STACK(view->root), FALSE);
    view->empty = adw_status_page_new();
    adw_status_page_set_title(ADW_STATUS_PAGE(view->empty), "暂无歌词");
    adw_status_page_set_icon_name(ADW_STATUS_PAGE(view->empty), "view-subtitles-symbolic");
    gtk_stack_add_child(GTK_STACK(view->root), view->empty);
    view->content = gtk_box_new(GTK_ORIENTATION_VERTICAL, 6);
    gtk_widget_set_margin_top(view->content, 12);
    gtk_widget_set_margin_bottom(view->content, 12);
    gtk_stack_add_child(GTK_STACK(view->root), view->content);

    GtkWidget *tools = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 6);
    gtk_widget_set_halign(tools, GTK_ALIGN_CENTER);
    gtk_box_append(GTK_BOX(view->content), tools);
    view->import = gtk_button_new_with_label("导入歌词");
    gtk_widget_add_css_class(view->import, "flat");
    gtk_box_append(GTK_BOX(tools), view->import);
    g_signal_connect(view->import, "clicked", G_CALLBACK(import_clicked), view);
    GtkWidget *timing = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0);
    gtk_widget_add_css_class(timing, "linked");
    gtk_box_append(GTK_BOX(tools), timing);
    view->offset = lyric_label(NULL, "numeric", FALSE);
    gtk_label_set_width_chars(GTK_LABEL(view->offset), 9);
    gtk_widget_set_tooltip_text(view->offset, "歌词时间偏移");
    gtk_box_append(GTK_BOX(timing), offset_button(view, "−", "歌词延后半秒", -1));
    gtk_box_append(GTK_BOX(timing), view->offset);
    gtk_box_append(GTK_BOX(timing), offset_button(view, "+", "歌词提前半秒", 1));
    view->feedback = lyric_label(NULL, "dim-label", TRUE);
    gtk_widget_set_visible(view->feedback, FALSE);
    gtk_box_append(GTK_BOX(view->content), view->feedback);

    TlLyricSpread *spread = g_object_new(tl_lyric_spread_get_type(), NULL);
    gtk_widget_set_vexpand(GTK_WIDGET(spread), TRUE);
    gtk_box_append(GTK_BOX(view->content), GTK_WIDGET(spread));
    spread->artwork = gtk_box_new(GTK_ORIENTATION_VERTICAL, 8);
    gtk_widget_set_parent(spread->artwork, GTK_WIDGET(spread));
    spread->cover = gtk_overlay_new();
    gtk_widget_set_parent(spread->cover, GTK_WIDGET(spread));
    gtk_widget_set_overflow(spread->cover, GTK_OVERFLOW_HIDDEN);
    gtk_widget_add_css_class(spread->cover, "tl-stage-cover");
    view->picture = gtk_picture_new();
    gtk_picture_set_content_fit(GTK_PICTURE(view->picture), GTK_CONTENT_FIT_CONTAIN);
    gtk_overlay_set_child(GTK_OVERLAY(spread->cover), view->picture);
    view->placeholder = gtk_image_new_from_icon_name("audio-x-generic-symbolic");
    gtk_image_set_pixel_size(GTK_IMAGE(view->placeholder), 48);
    gtk_widget_set_halign(view->placeholder, GTK_ALIGN_CENTER);
    gtk_widget_set_valign(view->placeholder, GTK_ALIGN_CENTER);
    gtk_overlay_add_overlay(GTK_OVERLAY(spread->cover), view->placeholder);
    view->title = lyric_label(NULL, "title-2", FALSE);
    view->artist = lyric_label(NULL, "dim-label", FALSE);
    gtk_box_append(GTK_BOX(spread->artwork), view->title);
    gtk_box_append(GTK_BOX(spread->artwork), view->artist);

    view->words = gtk_stack_new();
    spread->words = view->words;
    gtk_widget_set_parent(view->words, GTK_WIDGET(spread));
    gtk_stack_set_hhomogeneous(GTK_STACK(view->words), FALSE);
    gtk_stack_set_vhomogeneous(GTK_STACK(view->words), FALSE);
    gtk_stack_add_named(GTK_STACK(view->words), lyric_label("暂无歌词 · 可导入本地歌词", "dim-label", TRUE), "empty");
    view->scroll = gtk_scrolled_window_new();
    gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(view->scroll), GTK_POLICY_NEVER, GTK_POLICY_AUTOMATIC);
    gtk_stack_add_named(GTK_STACK(view->words), view->scroll, "rows");
    view->rows = gtk_box_new(GTK_ORIENTATION_VERTICAL, 16);
    gtk_widget_set_margin_top(view->rows, 12);
    gtk_widget_set_margin_bottom(view->rows, 12);
    gtk_widget_set_margin_start(view->rows, 10);
    gtk_widget_set_margin_end(view->rows, 10);
    gtk_widget_set_valign(view->rows, GTK_ALIGN_CENTER);
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(view->scroll), view->rows);
    g_signal_connect(gtk_scrolled_window_get_vadjustment(GTK_SCROLLED_WINDOW(view->scroll)),
                     "changed", G_CALLBACK(viewport_changed), view);

    g_signal_connect(view->root, "map", G_CALLBACK(lyrics_mapped), view);
    g_signal_connect(view->root, "unmap", G_CALLBACK(lyrics_unmapped), view);
    g_object_set_data_full(G_OBJECT(view->root), "tl-lyrics-state", view, free_lyrics);
    return view->root;
}

uintptr_t tl_lyrics_mapped_state(void)
{
    return lyrics_view && gtk_widget_get_mapped(lyrics_view->root) ? lyrics_view->state : 0;
}

void tl_lyrics_track(int present, int can_import, const char *title, const char *artist, const char *offset)
{
    TlLyrics *view = lyrics_view;
    gtk_stack_set_visible_child(GTK_STACK(view->root), present ? view->content : view->empty);
    gtk_widget_set_sensitive(view->import, can_import);
    gtk_label_set_text(GTK_LABEL(view->title), title);
    gtk_label_set_text(GTK_LABEL(view->artist), artist);
    gtk_label_set_text(GTK_LABEL(view->offset), offset);
}

void tl_lyrics_feedback(const char *text)
{
    gtk_label_set_text(GTK_LABEL(lyrics_view->feedback), text ? text : "");
    gtk_widget_set_visible(lyrics_view->feedback, text && text[0]);
}

void tl_lyrics_rows(const char *const *titles, const char *const *subtitles)
{
    TlLyrics *view = lyrics_view;
    view->active = -1;
    g_ptr_array_set_size(view->items, 0);
    GtkWidget *child;
    while ((child = gtk_widget_get_first_child(view->rows)))
        gtk_box_remove(GTK_BOX(view->rows), child);
    for (guint i = 0; titles[i]; ++i) {
        GtkWidget *row = gtk_box_new(GTK_ORIENTATION_VERTICAL, 3);
        GtkWidget *text = lyric_label(titles[i], "tl-stage-lyric", TRUE);
        gtk_widget_add_css_class(text, "dim-label");
        gtk_box_append(GTK_BOX(row), text);
        if (subtitles[i][0])
            gtk_box_append(GTK_BOX(row), lyric_label(subtitles[i], "dim-label", TRUE));
        gtk_box_append(GTK_BOX(view->rows), row);
        g_ptr_array_add(view->items, row);
    }
    gtk_adjustment_set_value(gtk_scrolled_window_get_vadjustment(GTK_SCROLLED_WINDOW(view->scroll)), 0);
    gtk_stack_set_visible_child_name(GTK_STACK(view->words), view->items->len > 0 ? "rows" : "empty");
}

void tl_lyrics_active(int row)
{
    TlLyrics *view = lyrics_view;
    if (view->active >= 0) {
        GtkWidget *old = g_ptr_array_index(view->items, view->active);
        gtk_widget_remove_css_class(old, "tl-stage-active");
        gtk_widget_add_css_class(gtk_widget_get_first_child(old), "dim-label");
    }
    view->active = row;
    if (row >= 0) {
        GtkWidget *next = g_ptr_array_index(view->items, row);
        gtk_widget_add_css_class(next, "tl-stage-active");
        gtk_widget_remove_css_class(gtk_widget_get_first_child(next), "dim-label");
        schedule_follow(view);
    }
}

void tl_lyrics_cover(const void *pixels, int width, int height, size_t stride, size_t size)
{
    GdkTexture *texture = NULL;
    if (pixels) {
        /* The shared model stores premultiplied BGRA, independent of host byte order. */
        GBytes *bytes = g_bytes_new(pixels, size);
        texture = gdk_memory_texture_new(width, height, GDK_MEMORY_B8G8R8A8_PREMULTIPLIED, bytes, stride);
        g_bytes_unref(bytes);
    }
    gtk_picture_set_paintable(GTK_PICTURE(lyrics_view->picture), texture ? GDK_PAINTABLE(texture) : NULL);
    gtk_widget_set_visible(lyrics_view->placeholder, texture == NULL);
    g_clear_object(&texture);
}
