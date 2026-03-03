package elasticsearch

import (
	"maps"
	"slices"

	"github.com/Tencent/WeKnora/internal/types"
)

// VectorEmbedding defines the Elasticsearch document structure for vector embeddings
type VectorEmbedding struct {
	Content         string    `json:"content"                    gorm:"column:content;not null"`
	SourceID        string    `json:"source_id"                  gorm:"column:source_id;not null"`
	SourceType      int       `json:"source_type"                gorm:"column:source_type;not null"`
	ChunkID         string    `json:"chunk_id"                   gorm:"column:chunk_id"`
	KnowledgeID     string    `json:"knowledge_id"               gorm:"column:knowledge_id"`
	KnowledgeBaseID string    `json:"knowledge_base_id"          gorm:"column:knowledge_base_id"`
	TagID           string    `json:"tag_id,omitempty"            gorm:"column:tag_id"`
	Embedding       []float32 `json:"embedding,omitempty"         gorm:"column:embedding;not null"`
	IsEnabled       bool      `json:"is_enabled"`
	Title           string    `json:"title,omitempty"`
	Description     string    `json:"description,omitempty"`
}

// VectorEmbeddingWithScore extends VectorEmbedding with similarity score
type VectorEmbeddingWithScore struct {
	VectorEmbedding
	Score float64 // Similarity score from vector search
}

// ToDBVectorEmbedding converts IndexInfo to Elasticsearch document format
func ToDBVectorEmbedding(embedding *types.IndexInfo, additionalParams map[string]interface{}) *VectorEmbedding {
	vector := &VectorEmbedding{
		Content:         embedding.Content,
		SourceID:        embedding.SourceID,
		SourceType:      int(embedding.SourceType),
		ChunkID:         embedding.ChunkID,
		KnowledgeID:     embedding.KnowledgeID,
		KnowledgeBaseID: embedding.KnowledgeBaseID,
		TagID:           embedding.TagID,
		IsEnabled:       embedding.IsEnabled,
		Title:           embedding.KnowledgeTitle,
		Description:     embedding.KnowledgeDescription,
	}
	// Add embedding data if available in additionalParams
	if additionalParams != nil && slices.Contains(slices.Collect(maps.Keys(additionalParams)), "embedding") {
		if embeddingMap, ok := additionalParams["embedding"].(map[string][]float32); ok {
			vector.Embedding = embeddingMap[embedding.SourceID]
		}
	}
	// Get is_enabled from additionalParams if available (highest priority override)
	if additionalParams != nil {
		if chunkEnabledMap, ok := additionalParams["chunk_enabled"].(map[string]bool); ok {
			if enabled, exists := chunkEnabledMap[embedding.ChunkID]; exists {
				vector.IsEnabled = enabled
			}
		}
	}
	return vector
}

// FromDBVectorEmbeddingWithScore converts Elasticsearch document to IndexWithScore domain model
func FromDBVectorEmbeddingWithScore(id string,
	embedding *VectorEmbeddingWithScore,
	matchType types.MatchType,
) *types.IndexWithScore {
	return &types.IndexWithScore{
		ID:              id,
		SourceID:        embedding.SourceID,
		SourceType:      types.SourceType(embedding.SourceType),
		ChunkID:         embedding.ChunkID,
		KnowledgeID:     embedding.KnowledgeID,
		KnowledgeBaseID: embedding.KnowledgeBaseID,
		Content:         embedding.Content,
		Score:           embedding.Score,
		MatchType:       matchType,
	}
}
