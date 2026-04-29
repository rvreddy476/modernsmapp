package store

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// --- Domain Models ---

type QAProfile struct {
	UserID          uuid.UUID `json:"user_id"`
	DisplayName     string    `json:"display_name"`
	Bio             string    `json:"bio"`
	ExpertiseAreas  []string  `json:"expertise_areas"`
	ReputationScore int       `json:"reputation_score"`
	QuestionCount   int       `json:"question_count"`
	AnswerCount     int       `json:"answer_count"`
	BestAnswerCount int       `json:"best_answer_count"`
	IsVerified      bool      `json:"is_verified"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Topic struct {
	ID            uuid.UUID  `json:"id"`
	Name          string     `json:"name"`
	Slug          string     `json:"slug"`
	Description   string     `json:"description"`
	IconURL       string     `json:"icon_url"`
	ParentTopicID *uuid.UUID `json:"parent_topic_id,omitempty"`
	QuestionCount int        `json:"question_count"`
	FollowerCount int        `json:"follower_count"`
	IsFeatured    bool       `json:"is_featured"`
	CreatedAt     time.Time  `json:"created_at"`
	IsFollowing   *bool      `json:"is_following,omitempty"`
}

type TopicAlias struct {
	ID       uuid.UUID `json:"id"`
	TopicID  uuid.UUID `json:"topic_id"`
	Alias    string    `json:"alias"`
	Language string    `json:"language"`
}

type CommunityScope struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Visibility    string    `json:"visibility"`
	CommunityType string    `json:"community_type,omitempty"`
}

type MirroredCommunity struct {
	ID                 uuid.UUID  `json:"id"`
	OwnerID            uuid.UUID  `json:"owner_id"`
	Name               string     `json:"name"`
	CommunityType      string     `json:"community_type"`
	Status             string     `json:"status"`
	QAQuestionCount    int        `json:"qa_question_count"`
	QAAnswerCount      int        `json:"qa_answer_count"`
	QAContributorCount int        `json:"qa_contributor_count"`
	LastQAActivityAt   *time.Time `json:"last_qa_activity_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	DeletedAt          *time.Time `json:"deleted_at,omitempty"`
}

type CommunityQASettings struct {
	CommunityID             uuid.UUID   `json:"community_id"`
	QAEnabled               bool        `json:"qa_enabled"`
	AskPermission           string      `json:"ask_permission"`
	AnswerPermission        string      `json:"answer_permission"`
	AutoSuggestTopics       bool        `json:"auto_suggest_topics"`
	SuggestedTopicIDs       []uuid.UUID `json:"suggested_topic_ids,omitempty"`
	RequireApproval         bool        `json:"require_approval"`
	WelcomeMessage          string      `json:"welcome_message"`
	TotalQuestionsCount     int         `json:"total_questions"`
	TotalAnswersCount       int         `json:"total_answers"`
	UniqueContributorsCount int         `json:"unique_contributors"`
	CreatedAt               time.Time   `json:"created_at"`
	UpdatedAt               time.Time   `json:"updated_at"`
}

type CommunityTopicAffinity struct {
	CommunityID    uuid.UUID  `json:"community_id"`
	Topic          Topic      `json:"topic"`
	QuestionCount  int        `json:"question_count"`
	AnswerCount    int        `json:"answer_count"`
	ViewCount      int        `json:"view_count"`
	AffinityScore  float64    `json:"affinity_score"`
	LastQuestionAt *time.Time `json:"last_question_at,omitempty"`
}

type CommunityTopicOption struct {
	Topic         Topic `json:"topic"`
	QuestionCount int   `json:"question_count"`
}

type Question struct {
	ID            uuid.UUID       `json:"id"`
	AuthorID      uuid.UUID       `json:"author_id"`
	CommunityID   *uuid.UUID      `json:"community_id,omitempty"`
	Title         string          `json:"title"`
	Body          string          `json:"body"`
	BodyHTML      string          `json:"body_html"`
	Slug          string          `json:"slug"`
	Status        string          `json:"status"`
	Visibility    string          `json:"visibility"`
	Language      string          `json:"language"`
	VoteScore     int             `json:"vote_score"`
	UpvoteCount   int             `json:"upvote_count"`
	DownvoteCount int             `json:"downvote_count"`
	AnswerCount   int             `json:"answer_count"`
	ViewCount     int             `json:"view_count"`
	FollowCount   int             `json:"follow_count"`
	IsAnswered    bool            `json:"is_answered"`
	BestAnswerID  *uuid.UUID      `json:"best_answer_id,omitempty"`
	ClosedReason  *string         `json:"closed_reason,omitempty"`
	ClosedBy      *uuid.UUID      `json:"closed_by,omitempty"`
	MergedIntoID  *uuid.UUID      `json:"merged_into_id,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	DeletedAt     *time.Time      `json:"deleted_at,omitempty"`
	Topics        []Topic         `json:"topics,omitempty"`
	Tags          []string        `json:"tags,omitempty"`
	Author        *QAProfile      `json:"author,omitempty"`
	ViewerVote    *string         `json:"viewer_vote,omitempty"`
	IsSaved       *bool           `json:"is_saved,omitempty"`
	IsFollowing   *bool           `json:"is_following,omitempty"`
	Community     *CommunityScope `json:"community,omitempty"`
	IsPinned      bool            `json:"is_pinned"`
	IsAnonymous   bool            `json:"is_anonymous,omitempty"`
}

type QuestionSummary struct {
	ID          uuid.UUID       `json:"id"`
	AuthorID    uuid.UUID       `json:"author_id"`
	CommunityID *uuid.UUID      `json:"community_id,omitempty"`
	Title       string          `json:"title"`
	Slug        string          `json:"slug"`
	Status      string          `json:"status"`
	VoteScore   int             `json:"vote_score"`
	AnswerCount int             `json:"answer_count"`
	ViewCount   int             `json:"view_count"`
	IsAnswered  bool            `json:"is_answered"`
	CreatedAt   time.Time       `json:"created_at"`
	Tags        []string        `json:"tags,omitempty"`
	Author      *QAProfile      `json:"author,omitempty"`
	Community   *CommunityScope `json:"community,omitempty"`
	IsPinned    bool            `json:"is_pinned"`
	Excerpt     string          `json:"excerpt,omitempty"`
	IsAnonymous bool            `json:"is_anonymous,omitempty"`
}

type Answer struct {
	ID             uuid.UUID         `json:"id"`
	QuestionID     uuid.UUID         `json:"question_id"`
	AuthorID       uuid.UUID         `json:"author_id"`
	Body           string            `json:"body"`
	BodyHTML       string            `json:"body_html"`
	VoteScore      int               `json:"vote_score"`
	UpvoteCount    int               `json:"upvote_count"`
	DownvoteCount  int               `json:"downvote_count"`
	IsBest         bool              `json:"is_best"`
	IsAccepted     bool              `json:"is_accepted"`
	CommentCount   int               `json:"comment_count"`
	ReferenceCount int               `json:"reference_count"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	DeletedAt      *time.Time        `json:"deleted_at,omitempty"`
	References     []AnswerReference `json:"references,omitempty"`
	Author         *QAProfile        `json:"author,omitempty"`
	ViewerVote     *string           `json:"viewer_vote,omitempty"`
	IsSaved        *bool             `json:"is_saved,omitempty"`
	IsAnonymous    bool              `json:"is_anonymous,omitempty"`
}

type AnswerReference struct {
	ID          uuid.UUID `json:"id"`
	AnswerID    uuid.UUID `json:"answer_id"`
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	SortOrder   int       `json:"sort_order"`
}

type AnswerComment struct {
	ID        uuid.UUID  `json:"id"`
	AnswerID  uuid.UUID  `json:"answer_id"`
	AuthorID  uuid.UUID  `json:"author_id"`
	Body      string     `json:"body"`
	VoteScore int        `json:"vote_score"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	Author    *QAProfile `json:"author,omitempty"`
}

type AnswerRequest struct {
	ID              uuid.UUID `json:"id"`
	QuestionID      uuid.UUID `json:"question_id"`
	RequesterID     uuid.UUID `json:"requester_id"`
	RequestedUserID uuid.UUID `json:"requested_user_id"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
}

type ReputationEvent struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"user_id"`
	EventType  string     `json:"event_type"`
	Points     int        `json:"points"`
	SourceType string     `json:"source_type"`
	SourceID   *uuid.UUID `json:"source_id,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type ContributorBadge struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	BadgeType string    `json:"badge_type"`
	BadgeName string    `json:"badge_name"`
	AwardedAt time.Time `json:"awarded_at"`
}

type ModerationReport struct {
	ID         uuid.UUID  `json:"id"`
	ReporterID uuid.UUID  `json:"reporter_id"`
	TargetType string     `json:"target_type"`
	TargetID   uuid.UUID  `json:"target_id"`
	Reason     string     `json:"reason"`
	Details    string     `json:"details"`
	Status     string     `json:"status"`
	ReviewedBy *uuid.UUID `json:"reviewed_by,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type ModerationAction struct {
	ID         uuid.UUID  `json:"id"`
	ReportID   *uuid.UUID `json:"report_id,omitempty"`
	ActorID    uuid.UUID  `json:"actor_id"`
	ActionType string     `json:"action_type"`
	TargetType string     `json:"target_type"`
	TargetID   uuid.UUID  `json:"target_id"`
	Reason     string     `json:"reason"`
	CreatedAt  time.Time  `json:"created_at"`
}
