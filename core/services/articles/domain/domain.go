// Package domain contains the articles service's domain types and errors.
// All types are aliases of the generated proto types — no parallel structs.
package domain

import articlesv1 "milton_prism/pkg/pb/gen/milton_prism/types/articles/v1"

type (
	Article        = articlesv1.Article
	Tag            = articlesv1.Tag
	ArticlesFilter = articlesv1.ArticlesFilter
	ArticleState   = articlesv1.ArticleState
	TagState       = articlesv1.TagState
)

const (
	ArticleStateUnspecified = articlesv1.ArticleState_ARTICLE_STATE_UNSPECIFIED
	ArticleStateActive      = articlesv1.ArticleState_ARTICLE_STATE_ACTIVE
	ArticleStateDeleted     = articlesv1.ArticleState_ARTICLE_STATE_DELETED
	TagStateUnspecified     = articlesv1.TagState_TAG_STATE_UNSPECIFIED
	TagStateActive          = articlesv1.TagState_TAG_STATE_ACTIVE
	TagStateDeleted         = articlesv1.TagState_TAG_STATE_DELETED
)
