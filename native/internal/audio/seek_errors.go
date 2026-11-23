package audio

import "errors"

// ErrOnlineSeekNeedsCache prevents a first seek from indexing an entire remote FLAC.
var ErrOnlineSeekNeedsCache = errors.New("online FLAC seek requires a local cache or embedded seek table")
