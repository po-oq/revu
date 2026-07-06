package store

import "context"

func (s *Store) SeedIfEmpty(ctx context.Context) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM threads`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	samples := []CreateThreadInput{
		{
			Type:          TypeMarkdown,
			Title:         "Markdown仕様レビュー",
			Body:          "# Markdown仕様レビュー\n\n```mermaid\ngraph TD\nA[Start] --> B[Review]\n```\n",
			OwnerDeviceID: "seed",
			AuthorName:    "名無し",
		},
		{
			Type:          TypeHTML,
			Title:         "HTMLモックレビュー",
			Body:          `<section><h1>HTML mock</h1><button onclick="this.textContent='clicked'">sandbox JS</button></section>`,
			OwnerDeviceID: "seed",
			AuthorName:    "名無し",
		},
		{
			Type:          TypeText,
			Title:         "テキストレビュー",
			Body:          "レビュー対象のプレーンテキストです。",
			OwnerDeviceID: "seed",
			AuthorName:    "名無し",
		},
		{
			Type:          TypeFile,
			Title:         "Fileレビュー",
			Body:          "",
			OwnerDeviceID: "seed",
			AuthorName:    "名無し",
		},
	}
	for _, sample := range samples {
		if _, err := s.CreateThread(ctx, sample); err != nil {
			return err
		}
	}
	return nil
}
