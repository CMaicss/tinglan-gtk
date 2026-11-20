#ifndef TINGLAN_GTK_LINUX_H
#define TINGLAN_GTK_LINUX_H

#include <stdint.h>
#include <stddef.h>

/* Runs the application on the GTK thread. The handle is an opaque Go token. */
int tl_run(uintptr_t handle, const char *collection_css);

/* Thread-safe: queue one deferred tlGoWake on GLib's default context.
 * Cancel only a source still pending, before releasing the Go handle. */
unsigned int tl_post(uintptr_t handle);
void tl_cancel(unsigned int id);

/* GTK-thread only. UI calls do nothing when there is no active window. */
void tl_present(void);
void tl_hide(void);
void tl_quit(void);
int tl_mapped(void);
void tl_set_status(const char *text);
void tl_set_preferences(int mode, int quality, double volume);
void tl_set_logo(const void *pixels, size_t length);
void tl_show_lyrics(int visible);
void tl_set_cover(const void *pixels, size_t length, int width, int height, int stride);
/* pos and dur are seconds; a nonpositive duration disables seeking. */
void tl_set_playback(const char *title, const char *artist, int playing,
                     double pos, double dur);
/* Splice a NULL-terminated array of UTF-8 "title\nartist" strings. GTK
 * copies the strings; NULL inserts nothing. Positions are zero-based. */
void tl_set_rows(unsigned int start, unsigned int remove, const char *const *rows);
void tl_reset_scroll(void);
int tl_history_visible(void);
void tl_set_history_rows(unsigned int remove, const char *const *rows);
int tl_queue_visible(void);
void tl_set_queue_rows(unsigned int start, unsigned int remove, const char *const *rows);
void tl_set_queue_index(int index);

#endif
