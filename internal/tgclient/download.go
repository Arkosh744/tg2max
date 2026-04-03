package tgclient

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

// DownloadMedia downloads a media file to destDir and returns the local file path.
// Returns empty string if media cannot be downloaded (unsupported type, nil, etc).
func (c *Client) DownloadMedia(ctx context.Context, media tg.MessageMediaClass, msgID int, destDir string) (string, error) {
	if c.api == nil {
		return "", fmt.Errorf("client not running")
	}

	dl := downloader.NewDownloader()

	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		return c.downloadPhoto(ctx, dl, m, msgID, destDir)
	case *tg.MessageMediaDocument:
		return c.downloadDocument(ctx, dl, m, msgID, destDir)
	default:
		return "", nil
	}
}

func (c *Client) downloadPhoto(ctx context.Context, dl *downloader.Downloader, media *tg.MessageMediaPhoto, msgID int, destDir string) (string, error) {
	if media.Photo == nil {
		return "", nil
	}
	photo, ok := media.Photo.(*tg.Photo)
	if !ok {
		return "", nil
	}

	// Pick the largest photo size
	var best *tg.PhotoSize
	var bestArea int
	for _, size := range photo.Sizes {
		if ps, ok := size.(*tg.PhotoSize); ok {
			area := ps.W * ps.H
			if area > bestArea {
				bestArea = area
				best = ps
			}
		}
	}
	if best == nil {
		return "", nil
	}

	loc := &tg.InputPhotoFileLocation{
		ID:            photo.ID,
		AccessHash:    photo.AccessHash,
		FileReference: photo.FileReference,
		ThumbSize:     best.Type,
	}

	filename := fmt.Sprintf("%d_photo.jpg", msgID)
	path := filepath.Join(destDir, filename)

	if _, err := dl.Download(c.api, loc).ToPath(ctx, path); err != nil {
		return "", fmt.Errorf("download photo msg=%d: %w", msgID, err)
	}
	return path, nil
}

func (c *Client) downloadDocument(ctx context.Context, dl *downloader.Downloader, media *tg.MessageMediaDocument, msgID int, destDir string) (string, error) {
	if media.Document == nil {
		return "", nil
	}
	doc, ok := media.Document.(*tg.Document)
	if !ok {
		return "", nil
	}

	// Extract filename from attributes
	filename := fmt.Sprintf("%d_document", msgID)
	for _, attr := range doc.Attributes {
		if fa, ok := attr.(*tg.DocumentAttributeFilename); ok {
			filename = fmt.Sprintf("%d_%s", msgID, fa.FileName)
			break
		}
	}

	loc := &tg.InputDocumentFileLocation{
		ID:            doc.ID,
		AccessHash:    doc.AccessHash,
		FileReference: doc.FileReference,
	}

	path := filepath.Join(destDir, filename)
	if _, err := dl.Download(c.api, loc).ToPath(ctx, path); err != nil {
		return "", fmt.Errorf("download doc msg=%d: %w", msgID, err)
	}
	return path, nil
}
