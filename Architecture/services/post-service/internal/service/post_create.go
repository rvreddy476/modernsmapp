// post_create.go holds CreatePost, the one service entry point that makes a
// new post (every content type) live or staged. Moved out of post.go
// unchanged.

package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

func (s *Service) CreatePost(ctx context.Context, input *CreatePostInput) (*postgres.Post, error) {
	// Non-empty and length ceiling, before anything is written (C-LB-1.3).
	// Server-side because a hostile or older client will not enforce it.
	if err := ValidatePostContent(input.Text, len(input.MediaIDs)); err != nil {
		return nil, err
	}
	// Scheduling window, before anything is written (schedule.go).
	if err := ValidatePublishAt(input.PublishAt, time.Now()); err != nil {
		return nil, err
	}
	// The details screen's fields, before anything is written
	// (post_screen_fields.go): every one is a statement about the request.
	coverMediaID, err := ResolvePostCover(input.PostType, input.MediaIDs, input.CoverMediaID, input.CoverIndex)
	if err != nil {
		return nil, err
	}
	remixSetting, err := normalizeCreateEnum(input.RemixSetting, PostRemixSettings, "allow", ErrInvalidRemixSetting)
	if err != nil {
		return nil, err
	}
	commentAcc, err := normalizeCreateEnum(input.CommentAccess, PostCommentAccesses, "everyone", ErrInvalidCommentAccess)
	if err != nil {
		return nil, err
	}
	linkURL, err := NormalizeLinkURL(input.LinkURL)
	if err != nil {
		return nil, err
	}
	locationPlaceID, err := NormalizeLocationPlaceID(input.LocationPlaceID)
	if err != nil {
		return nil, err
	}
	// Author standing, before anything is written (publish_standing.go):
	// a suspended author gets 403 AUTHOR_SUSPENDED, an unanswerable
	// trust-safety 503 — never a post.
	if err := s.requirePublishStanding(ctx, input.AuthorID); err != nil {
		return nil, err
	}

	// DURABLE IDEMPOTENCY — fast path (C-LB-3.3).
	//
	// An ordinary retry is answered here without redoing validation, media
	// authority, spam scoring and event construction. This read is only an
	// optimisation: the authority is the unique index inside
	// CreatePostWithEventIdempotent, because another request can claim the key
	// between this lookup and that insert.
	if input.CreateKey != "" {
		postID, fingerprint, found, lookupErr := s.lookupCreateIdempotency(
			ctx, input.AuthorID, input.CreateKey)
		if lookupErr != nil {
			// Fail closed: an unreadable authority must not become "assume new".
			return nil, lookupErr
		}
		if found {
			if fingerprint != input.CreateFingerprint {
				return nil, ErrCreateKeyReused
			}
			existing, getErr := s.pgStore.GetPost(ctx, postID)
			if getErr != nil {
				return nil, fmt.Errorf("replay created post: %w", getErr)
			}
			if existing != nil {
				s.AttachAuthorActions(ctx, input.AuthorID, []*postgres.Post{existing})
			}
			return existing, nil
		}
	}

	// Validate the distribution policy up front so a malformed/unsupported
	// policy fails the whole request (400) before any row is written —
	// never silently ignored (Codex P0-1).
	distPolicy, err := ParseDistributionPolicy(input.Distribution)
	if err != nil {
		return nil, err
	}

	contentType := input.ContentType
	if contentType == "" {
		contentType = "post"
	}

	// Normalize legacy content types from old clients
	contentType = normalizeLegacyContentType(contentType)

	// Validate content_type
	if !validContentTypes[contentType] {
		return nil, fmt.Errorf("%w %q: must be post, poll, flick, or long_video", ErrInvalidContentType, contentType)
	}

	// Long-video uploads paused (copyright repeat-uploader rule,
	// copyright_title_review.go): one more field of the standing answer
	// requirePublishStanding already cached. Before anything is written;
	// reels and posts are never paused by it.
	if err := s.requireLongVideoUploads(ctx, input.AuthorID, contentType); err != nil {
		return nil, err
	}

	// Video credits (credits.go): every value, then the kind — the INTENT,
	// so only what the author posted as a long video may carry them (a
	// plain post with a video attached stays a post). Before anything is
	// written.
	videoCredits, productionHouse, err := validatedVideoCredits(contentType, input.VideoCredits, s.clock())
	if err != nil {
		return nil, err
	}

	// Tube: a long video is listed under a channel, so the account must have
	// created one first (founder rule, 2026-09-05). Reels/flicks are not gated.
	if err := s.gateVideoBehindChannel(ctx, input.AuthorID, contentType); err != nil {
		return nil, err
	}

	// Tube: a long video is listed by its title, so it must have one; the
	// ceiling applies to every kind of post. Stored trimmed.
	title := strings.TrimSpace(input.Title)
	if err := ValidateTitle(contentType, title); err != nil {
		return nil, err
	}

	// Category is stored in its slug form for every content type, and flicks
	// are additionally held to the closed taxonomy (categories.go). This is
	// the single write site for posts.category: the create route and all
	// three draft-publish paths build a CreatePostInput and land here, so
	// feed-service's exact-match category pages see one spelling.
	category, catErr := resolveCreateCategory(contentType, input.Category)
	if catErr != nil {
		return nil, catErr
	}

	taggedUsers, tagErr := NormalizeTaggedUsers(input.AuthorID, input.TaggedUserIDs)
	if tagErr != nil {
		return nil, tagErr
	}

	// Members-only from birth (members_tiers.go): the tier must be one of
	// the author's active tiers. A tier list monetization-service cannot
	// answer refuses the create (503) — never a post created open.
	if err := s.ValidateTierForAuthor(ctx, input.AuthorID, input.TierRequiredID); err != nil {
		return nil, err
	}

	// The Action button (action_links.go): label, URL rules, eligibility
	// and the safety check, before anything is written; the link is
	// inserted in the post's own transaction (insertPostTx).
	var actionWrite *postgres.ActionLinkWrite
	if input.Action != nil {
		w, err := s.prepareAction(ctx, input.AuthorID, input.Action)
		if err != nil {
			return nil, err
		}
		w.ID = uuid.New()
		actionWrite = w
	}

	// REMOVED 21 Sep: "Trusted Circle after-hours protection", which
	// auto-rewrote a defaulted visibility to `trusted` between 22:00 and
	// 06:00 when the author had tc_after_hours_posts on.
	//
	// The trusted/close-friends audience was retired with the Trusted
	// Circle tier (graph-service migration 012). Leaving this in would
	// have been worse than removing it: a late-night post would still be
	// rewritten to an audience that now resolves to NOBODY, so the author
	// would silently publish to themselves. The tc_* settings it read are
	// gone too.

	postType := input.PostType
	if postType == "" {
		postType = "text"
	}
	appOrigin := input.AppOrigin
	if appOrigin == "" {
		appOrigin = "postbook"
	}

	// Extract hashtags from text (cap at 20 per design spec)
	hashtags := extractHashtags(input.Text)
	if len(hashtags) > 20 {
		hashtags = hashtags[:20]
	}
	// Merge the studio's explicit `hashtags` field (explicit_tags.go):
	// validated here so a bad tag fails the request before any write.
	explicitTags, err := NormalizeExplicitHashtags(input.Hashtags)
	if err != nil {
		return nil, err
	}
	hashtags = mergeTags(hashtags, explicitTags, maxMergedHashtags)
	hashtags = s.filterBlockedHashtags(ctx, hashtags)

	// Extract @mentions from text and merge the explicit `mentions` field.
	mentions := extractMentions(input.Text)
	explicitMentions, err := NormalizeExplicitMentions(input.Mentions)
	if err != nil {
		return nil, err
	}
	mentions = mergeTags(mentions, explicitMentions, maxMergedMentions)

	// "Who can mention or tag me" (mention_tag_privacy.go): a mention or tag
	// of someone who does not allow it is dropped here, silently — the
	// mention stays in the text as plain text — so it is never stored,
	// linked or notified, and the create never fails over it.
	mentions = s.allowedMentions(ctx, input.AuthorID, mentions)
	taggedUsers = s.allowedTags(ctx, input.AuthorID, taggedUsers)

	// Default reel metadata values
	lang := input.Language
	if lang == "" {
		lang = "en"
	}
	license := input.License
	if license == "" {
		license = "standard"
	}
	// remix_setting and comment_access were validated and defaulted at the
	// top (normalizeCreateEnum): they used to be stored as sent.
	commentMod := input.CommentModeration
	if commentMod == "" {
		commentMod = "none"
	}
	origVol := input.OriginalAudioVol
	if origVol == 0 {
		origVol = 1.0
	}
	overlayVol := input.OverlayAudioVol
	if overlayVol == 0 {
		overlayVol = 1.0
	}

	newPostID := uuid.New()
	if input.PostID != nil && *input.PostID != uuid.Nil {
		newPostID = *input.PostID
	}
	p := &postgres.Post{
		ID:                newPostID,
		AuthorID:          input.AuthorID,
		Text:              input.Text,
		Visibility:        input.Visibility,
		ContentType:       contentType,
		Feeling:           input.Feeling,
		Activity:          input.Activity,
		ActivityDetail:    input.ActivityDetail,
		RichText:          input.RichText,
		NoComments:        input.NoComments,
		NoLikes:           input.NoLikes,
		Hashtags:          hashtags,
		LocationName:      input.LocationName,
		LocationLat:       input.LocationLat,
		LocationLng:       input.LocationLng,
		PostType:          postType,
		AppOrigin:         appOrigin,
		ShareToPostbook:   input.ShareToPostbook,
		Title:             title,
		Tags:              input.Tags,
		Category:          category,
		Language:          lang,
		SEOTitle:          input.SEOTitle,
		PaidPromotion:     input.PaidPromotion,
		AlteredContent:    input.AlteredContent,
		IsMadeForKids:     input.IsMadeForKids,
		License:           license,
		AllowEmbedding:    input.AllowEmbedding,
		PublishToFeed:     input.PublishToFeed,
		RemixSetting:      remixSetting,
		CommentModeration: commentMod,
		CommentAccess:     commentAcc,
		RecordingDate:     input.RecordingDate,
		RecordingLocation: input.RecordingLocation,
		CoverMediaID:      coverMediaID,
		OriginalAudioVol:  origVol,
		OverlayAudioVol:   overlayVol,
		HideShare:         input.HideShare,
		AllowDownload:     input.AllowDownload,
		TaggedUserIDs:     taggedUsers,
		MentionUsernames:  mentions,
		PublishAt:         input.PublishAt,
		CreatedAt:         time.Now(),
		IsBranded:         input.IsBranded,
		AIGenerated:       input.AIGenerated,
		LinkURL:           linkURL,
		LocationPlaceID:   locationPlaceID,
		Genres:            videoCredits.Genres,
		ReleaseYear:       videoCredits.ReleaseYear,
		ProductionHouse:   productionHouse,
		// Written by the insert itself (posts.tier_required_id), in the
		// same transaction as the row and its PostCreated outbox event.
		TierRequiredID: copyTierID(input.TierRequiredID),
		ActionWrite:    actionWrite,
	}

	// Persist the validated policy verbatim; rev 1 marks "has a policy",
	// rev 0 marks legacy rows that never carried one.
	//
	// P1-1: when there is no typed policy but the old client explicitly
	// asked for a distribution outcome, materialize that intent into the
	// canonical policy. Without this the row and the event would both
	// fall back to "main_feed=true" and silently override the creator.
	effectivePolicy := distPolicy
	if effectivePolicy == nil {
		effectivePolicy = PolicyFromLegacy(input.LegacyDistribution)
	}
	if effectivePolicy != nil {
		stored, mErr := MarshalPolicy(effectivePolicy)
		if mErr != nil {
			return nil, mErr
		}
		p.Distribution = stored
		p.DistributionRev = 1
	}

	// AUTHORITY BEFORE ATTACHMENT (Slice C, C-LB-4).
	//
	// Ordinary create-post used to look up media KIND only, and silently
	// treated a missing row as an image. It never established that the caller
	// uploaded the asset, that processing had finished, or that moderation had
	// passed. Any authenticated user could therefore attach another user's
	// media by id, or publish an asset that was still processing or had already
	// been rejected by safety review.
	//
	// Thread creation already got this right (`threads.go`, Codex P1-6) using
	// the same batched query. The ordinary path is the one people actually use.
	//
	// The FK added by migration 030 prevents a DANGLING row; it says nothing
	// about who owns the media or whether it is safe to publish. Those are
	// different questions and only this check answers them.
	if err := s.verifyMediaAuthority(
		ctx, input.AuthorID, input.MediaIDs, contentType, postType,
	); err != nil {
		return nil, err
	}
	// The cover answers the same questions as an attachment (cover_guard.go):
	// it used to be stored as sent, a stranger's asset included.
	if coverMediaID != nil {
		if err := s.verifyCoverOnWrite(ctx, input.AuthorID, *coverMediaID); err != nil {
			return nil, err
		}
	}

	// Attach media in a single round trip — audit H1.
	// Previously this loop did 1 SELECT per media-id (kind), plus a
	// second SELECT per video (duration), plus a third SELECT for
	// dimensions of the first video. For N media that's ~2N+1
	// queries. BatchGetMediaMetadata folds it into one.
	mediaMeta, mediaErr := s.pgStore.BatchGetMediaMetadata(ctx, input.MediaIDs)
	if mediaErr != nil {
		// Fall back to the per-row helpers below if the batch query
		// fails — keeps post creation working through a transient
		// DB hiccup, just at the old query cost.
		log.Printf("Warning: batch media metadata lookup failed; falling back to per-row: %v", mediaErr)
		mediaMeta = nil
	}

	var maxDuration int
	for _, mediaID := range input.MediaIDs {
		var kind string
		var dur int
		if meta, ok := mediaMeta[mediaID]; ok {
			kind = meta.Kind
			dur = meta.DurationSeconds
		} else {
			// Either the batch failed entirely or the row didn't
			// exist in media_assets. Preserve the legacy
			// "default to image" behaviour from ResolveMediaKind.
			if mediaMeta == nil {
				kind = s.pgStore.ResolveMediaKind(ctx, mediaID)
				if kind == "video" {
					dur = s.pgStore.ResolveMediaDuration(ctx, mediaID)
				}
			} else {
				kind = "image"
			}
		}
		// Slice C / C-CLB-3: the create response carries the accessibility
		// decision the author just made. The composer navigates straight to
		// the post it created, so without this the first render of a brand-new
		// image post is the one render guaranteed to be unlabelled.
		//
		// Absent metadata leaves both zero: no description and not decorative,
		// which is "nobody said" — the state a renderer must treat as missing
		// rather than as an explicit decorative mark.
		meta := mediaMeta[mediaID]
		p.Media = append(p.Media, postgres.PostMedia{
			MediaID:       mediaID,
			Kind:          kind,
			AltText:       meta.AltText,
			AltDecorative: meta.AltDecorative,
		})
		if kind == "video" && dur > maxDuration {
			maxDuration = dur
		}
	}

	// Decide the content_type of a post that carries a video. A reel is what
	// the author posted as a reel; a video is what the author posted as a
	// video (founder, 2026-09-04/05) — and a post is a post (founder,
	// 2026-09-25): a plain "post" with a video stays in the feed rather than
	// being classified into Reels or Tube from the measurement. See
	// resolveVideoContentType.
	var videoMediaID uuid.UUID
	hasVideo := false
	for _, m := range p.Media {
		if m.Kind == "video" {
			videoMediaID = m.MediaID
			hasVideo = true
			break
		}
	}
	// P0-6: locate the voice attachment (media_assets.file_type='audio'
	// surfaces as kind "audio") and classify the post as a voice post.
	var voiceMediaID uuid.UUID
	for _, m := range p.Media {
		if m.Kind == "audio" {
			voiceMediaID = m.MediaID
			break
		}
	}
	if voiceMediaID != uuid.Nil && !hasVideo {
		p.ContentType = "voice"
		contentType = "voice"
	}
	// videoW/videoH are the dimensions transcode measured (0 when pending).
	// Reuse the batch when available; fall back to the per-row helper for
	// the unlikely batch-failed-but-loop-succeeded path.
	var videoW, videoH int
	if hasVideo {
		if meta, ok := mediaMeta[videoMediaID]; ok {
			videoW, videoH = meta.Width, meta.Height
		} else {
			videoW, videoH, _ = s.pgStore.ResolveMediaDimensions(ctx, videoMediaID)
		}
		// Persisting `explicit` is what tells the MediaTranscodeConsumer
		// to leave the kind alone once the measurement lands. A post is
		// reported explicit for that reason: it must not be rewritten into
		// a flick or long_video by a duration and a frame size.
		p.ContentType, p.ContentTypeExplicit = resolveVideoContentType(contentType, maxDuration, videoW, videoH)
	}

	// Attach poll
	if input.Poll != nil {
		var endsAt *time.Time
		if input.Poll.DurationHours != nil && *input.Poll.DurationHours > 0 {
			t := time.Now().Add(time.Duration(*input.Poll.DurationHours) * time.Hour)
			endsAt = &t
		}
		opts := make([]postgres.PollOption, len(input.Poll.Options))
		for i, label := range input.Poll.Options {
			opts[i] = postgres.PollOption{Label: label}
		}
		p.Poll = &postgres.PollData{
			Question:       input.Poll.Question,
			AllowsMultiple: input.Poll.AllowsMultiple,
			EndsAt:         endsAt,
			Options:        opts,
		}
	}

	// Spam detection
	spamResult := s.spam.Check(ctx, input.AuthorID.String(), input.Text, len(input.MediaIDs))
	if spamResult.Score > 0.95 {
		return nil, fmt.Errorf("content rejected: %s", spamResult.Reason)
	}
	reviewStatus := "approved"
	if spamResult.Score > 0.7 {
		reviewStatus = "flagged"
		// Emit spam detection event (best-effort)
		if s.producer != nil {
			go s.producer.PublishSpamDetected(context.Background(), input.AuthorID, spamResult.Reason, spamResult.Score)
		}
	}

	// reels/posttube items 2+3 — video publish gate: a video post is not
	// publicly visible until its media is transcoded AND content-scanned.
	// If the media is already ready, finalize the verdict now; otherwise
	// hold it 'pending' and the MediaTranscodeConsumer flips it when
	// transcode completes. The chunk-1 read-filters already hide every
	// non-'approved' post, so no event-flow change is needed.
	if reviewStatus == "approved" && isVideoContentType(p.ContentType) && videoMediaID != uuid.Nil {
		reviewStatus = s.gateVideoReviewStatus(ctx, videoMediaID)
	}
	// P0-6: voice posts stay out of public surfaces until baseline audio
	// safety completes. Same 'pending' mechanism as the video gate — the
	// read filters already hide every non-approved post.
	if reviewStatus == "approved" && isVoiceContentType(p.ContentType) && voiceMediaID != uuid.Nil {
		reviewStatus = s.gateVoiceReviewStatus(ctx, voiceMediaID)
	}
	// Optional: send every video to human review, not just spam-flagged. Covers
	// 'pending' (still transcoding) too — otherwise the transcode consumer would
	// later flip pending→approved and publish it without review. The reviewer
	// watches the uploaded media directly.
	if s.reviewAllVideos && isVideoContentType(p.ContentType) &&
		(reviewStatus == "approved" || reviewStatus == "pending") {
		reviewStatus = "flagged"
	}
	p.ReviewStatus = reviewStatus

	// Video credits: which tags may link an account, decided before the
	// transaction (it asks identity-profile and graph-service); the insert
	// writes them, spends the tagging limit and announces the requests in
	// the post's own transaction (store/postgres/credits.go).
	// Every long video being created carries a write (possibly empty), which
	// is also how its PostCreated knows the list is this one.
	if isLongVideoContentType(contentType) {
		w := &postgres.CreditsWrite{}
		if len(videoCredits.Credits) > 0 {
			w = s.resolveCreditCandidates(ctx, input.AuthorID, videoCredits.Credits)
		}
		w.PostTitle = title
		p.CreditsWrite = w
	}

	// Build the PostCreated event BEFORE the insert so the outbox row
	// commits in the same transaction as the post (Codex P0-1: event and
	// row are atomic; closes the old commit→outbox dual-write window).
	//
	// A SCHEDULED post (publish_at set) emits nothing here: no PostCreated,
	// so no feed fan-out, no search index, no follower or subscriber
	// notification. The schedule worker emits the identical event — built by
	// the same buildPostCreatedPayload — when it publishes (schedule.go).
	scheduled := p.PublishAt != nil
	p.IsScheduled = scheduled

	// Copyright title review (copyright_title_review.go): a long video over
	// 20 minutes naming a catalogue title, by a non-owner, is published
	// HELD — the hold commits with the post and PostCreated is withheld, so
	// nothing fans out, indexes or notifies until a reviewer clears it. A
	// scheduled post is judged when it goes live (PublishScheduled). A held
	// post may not be members-only (409 COPYRIGHT_REVIEW).
	if !scheduled {
		if err := s.applyCreateTitleHold(ctx, p, maxDuration, hasVideo); err != nil {
			return nil, err
		}
	}
	held := p.TitleReviewWrite != nil

	// Creation is always revision 1; later transitions increment.
	createEventType, createPayload := s.createdEventFor(ctx, p, effectivePolicy, maxDuration, videoH)

	// The post, its outbox event and the durable idempotency claim commit
	// together or not at all (C-LB-3.2).
	var idem *postgres.CreateIdempotency
	if input.CreateKey != "" {
		idem = &postgres.CreateIdempotency{
			ActorID:     input.AuthorID,
			ClientKey:   input.CreateKey,
			Fingerprint: input.CreateFingerprint,
		}
	}
	// The cover is claimed with media-service before the row names it
	// (media_claims.go); the write and its compensation run under the
	// cover's holder lock.
	if err := s.writeCreatedPostClaimed(ctx, p, createEventType, createPayload, idem); err != nil {
		// A concurrent request won the key. Whether it was the same intent
		// decides between replaying its post and refusing the reuse.
		var replay postgres.ErrCreateKeyReplay
		if errors.As(err, &replay) {
			existing, getErr := s.pgStore.GetPost(ctx, replay.PostID)
			if getErr != nil {
				return nil, fmt.Errorf("replay created post: %w", getErr)
			}
			if err := s.attachMediaState(ctx, []*postgres.Post{existing}); err != nil {
				return nil, err
			}
			s.AttachAuthorActions(ctx, input.AuthorID, []*postgres.Post{existing})
			return existing, nil
		}
		if errors.Is(err, postgres.ErrCreateKeyConflict) {
			return nil, ErrCreateKeyReused
		}
		return nil, err
	}

	// Record the auto-moderation verdict for video content (audit trail).
	// Skipped while 'pending' — the transcode consumer records the
	// terminal verdict when it finalizes the gate.
	if isVideoContentType(p.ContentType) && reviewStatus != "pending" {
		s.RecordVideoModeration(ctx, p.ID, reviewStatus, spamResult.Score)
	}

	// Route flagged video content to the human-review queue (best-effort).
	if reviewStatus == "flagged" {
		s.enqueueForReview(p, spamResult.Score)
	}

	// Persist the merged @mentions (caption + explicit field) to
	// post_mentions. Scheduled posts too: the rows are a join, not a
	// notification — user.mentioned is only emitted at publish time.
	s.storeMentions(ctx, p.ID, p.ContentType, mentions)

	// Create video_metadata for video content types.
	//
	// ALWAYS for a video attachment, even before transcode has measured it
	// (instant publish, 2026-09-04). The row is the join the
	// MediaTranscodeConsumer uses to find this post when the transcode
	// completes — to flip the `pending` review gate, wire the HLS URL and
	// reclassify. A post created while its media was still processing used
	// to get no row at all, so it could never be released.
	if videoMediaID != uuid.Nil {
		// upload_status starts at "pending" and is flipped to "ready" by
		// the MediaTranscodeCompleted consumer — which is the ONLY other
		// writer of the column. That made the ordering load-bearing in a
		// way no client was told about: a browser that uploaded, waited
		// for the transcode (the natural thing to do, since you want a
		// preview and a duration before filling in the details), and only
		// then created the post, produced a row the completion event had
		// already flown past. Nothing would ever set it, so
		// GET /v1/videos/:id reported a fully-transcoded video as
		// "pending" forever and POST /v1/videos/:id/publish answered 409
		// NOT_READY for the life of the post.
		//
		// The transcode having already finished is not a missed event, it
		// is a fact we can read: seed the column from the live
		// media_assets row instead of asserting a state we haven't
		// checked. A still-processing asset is unchanged — "pending", and
		// the consumer flips it as before.
		uploadStatus := "pending"
		if meta, ok := mediaMeta[videoMediaID]; ok && meta.ProcessingStatus == mediaReady {
			uploadStatus = mediaReady
		}
		vm := &postgres.VideoMetadata{
			PostID:       p.ID,
			UploadStatus: uploadStatus,
			MediaAssetID: &videoMediaID,
		}
		// final_category is the kind the post *is* — what the author chose,
		// or the measurement when they chose nothing (resolveVideoContentType
		// already settled that on p.ContentType). computed_category is the
		// measurement alone, kept for analytics: how many author-flicks are
		// landscape, how many author-videos are short verticals.
		vm.FinalCategory = p.ContentType
		if maxDuration > 0 {
			category, orientation := ClassifyVideo(float64(maxDuration), videoW, videoH)
			vm.DurationSeconds = float64(maxDuration)
			vm.Width = &videoW
			vm.Height = &videoH
			vm.Orientation = orientation
			vm.ComputedCategory = category
		} else {
			// Duration unknown: record the provisional answer; the
			// MediaTranscodeConsumer rewrites the measurement once the
			// transcode reports duration + dimensions.
			vm.ComputedCategory = p.ContentType
			vm.Orientation = deriveOrientation(0, 0)
			if p.ContentType == "flick" {
				vm.Orientation = "portrait"
			}
		}
		if err := s.pgStore.CreateVideoMetadata(ctx, vm); err != nil {
			log.Printf("Warning: failed to create video_metadata for post %s: %v", p.ID, err)
		}
	}

	// Invalidate author content counts cache
	if s.rdb != nil {
		s.rdb.Del(ctx, fmt.Sprintf("post:author-counts:%s", input.AuthorID))
	}

	// PostCreated rides the same transaction as the post row (see
	// CreatePostWithEventIdempotent above) — the outbox worker publishes it
	// to Kafka with retry until success. Everything else a public post does
	// at birth — user.mentioned, trending and hashtag counters, the live
	// pub/sub — is announcePublishedPost, and a scheduled post does none of
	// it until the worker publishes it.
	//
	// The worker's 5s tick stays the floor and the safety net, but waiting
	// it out delayed every post before fan-out had even begun, so we tell it
	// there is work now. Non-blocking, and a no-op if a drain is pending.
	s.NudgeOutbox()
	if held {
		// The hold committed with the row; the author's own reads see it.
		p.ActiveRestrictionCount = 1
		slog.Info("copyright title review: publication held", "post_id", p.ID, "author_id", p.AuthorID,
			"matched_title", p.TitleReviewWrite.MatchedTitle, "awaiting_duration", p.TitleReviewWrite.AwaitingDuration)
	}
	if !scheduled && !held {
		s.announcePublishedPost(p, mentions)
	}

	// The create response is the first render of the post the composer
	// navigates to. It carries the per-media pipeline state and
	// is_processing so the client can show "uploading… improving quality"
	// instead of a broken player.
	if err := s.attachMediaState(ctx, []*postgres.Post{p}); err != nil {
		return nil, err
	}
	// The author's view of the action just written (url and status
	// included), built from the write: no read back.
	if actionWrite != nil {
		p.Action = ActionViewFor(&postgres.ActionLink{
			ID: actionWrite.ID, AuthorID: p.AuthorID, URL: actionWrite.URL, Domain: actionWrite.Domain,
			Label: actionWrite.Label, Status: postgres.ActionStatusActive, SafetyStatus: actionWrite.SafetyStatus,
		}, true)
	}
	return p, nil
}
