-- =============================================================================
-- SEED DATA for identity_db
-- =============================================================================
--
-- HOW TO USE (3 steps in pgAdmin):
--
--   1. Run init-databases.sql  →  creates identity_db
--   2. Connect to identity_db, then run database/setup-all.sql  →  creates ALL tables
--   3. Still connected to identity_db, run THIS file  →  inserts test data
--
-- =============================================================================
-- Users:
--   user1: b2e06bd7-fa13-4f05-94cc-8973bcafe892  (johndoe, user1@example.com)
--   user2: a1b2c3d4-e5f6-7890-abcd-ef1234567890  (janedoe, user2@example.com)
--   user3: c3d4e5f6-a1b2-3456-7890-abcdef123456   (bobsmith, user3@example.com)
--   All passwords: password123
-- =============================================================================


-- =====================
-- auth.users
-- =====================
INSERT INTO auth.users (user_id, email, phone, password_hash, created_at)
VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'user1@example.com', NULL,
     '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy', '2026-01-15T10:00:00Z'),
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'user2@example.com', NULL,
     '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy', '2026-01-15T10:01:00Z'),
    ('c3d4e5f6-a1b2-3456-7890-abcdef123456', 'user3@example.com', '+15551234567',
     '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy', '2026-01-15T10:02:00Z')
ON CONFLICT DO NOTHING;


-- =====================
-- usr.users
-- =====================
INSERT INTO usr.users (id, status, is_verified, created_at, updated_at)
VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'active', TRUE,  '2026-01-15T10:00:00Z', '2026-02-01T12:00:00Z'),
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'active', TRUE,  '2026-01-15T10:01:00Z', '2026-01-20T09:00:00Z'),
    ('c3d4e5f6-a1b2-3456-7890-abcdef123456', 'active', FALSE, '2026-01-15T10:02:00Z', '2026-01-15T10:02:00Z')
ON CONFLICT DO NOTHING;


-- =====================
-- usr.user_settings
-- =====================
INSERT INTO usr.user_settings (user_id, account_visibility, allow_messages_from, allow_comments_from, created_at, updated_at)
VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'public', 'everyone', 'everyone', '2026-01-15T10:00:00Z', '2026-01-15T10:00:00Z'),
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'public', 'everyone', 'everyone', '2026-01-15T10:01:00Z', '2026-01-15T10:01:00Z'),
    ('c3d4e5f6-a1b2-3456-7890-abcdef123456', 'public', 'followers', 'everyone', '2026-01-15T10:02:00Z', '2026-01-15T10:02:00Z')
ON CONFLICT DO NOTHING;


-- =====================
-- profile.profiles
-- =====================
INSERT INTO profile.profiles (user_id, username, display_name, first_name, last_name, bio, dob, gender,
                              avatar_media_id, cover_media_id, category, profession, website, location,
                              badge_flags, created_at, updated_at)
VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'johndoe', 'John Doe',
     'John', 'Doe',
     'Full-stack developer & open-source enthusiast. Building the future one commit at a time.',
     '1995-06-15', 'male',
     '00000000-0000-4000-a000-000000000001', '00000000-0000-4000-a000-000000000002',
     'personal', 'Software Engineer', 'https://johndoe.dev', 'San Francisco, CA',
     3,  -- BADGE_VERIFIED | BADGE_CREATOR
     '2026-01-15T10:00:00Z', '2026-02-01T12:00:00Z'),

    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'janedoe', 'Jane Doe',
     'Jane', 'Doe',
     'Designer & photographer. Capturing moments in every frame.',
     '1998-03-22', 'female',
     '00000000-0000-4000-a000-000000000003', NULL,
     'personal', 'UX Designer', 'https://janedoe.design', 'New York, NY',
     1,  -- BADGE_VERIFIED
     '2026-01-15T10:01:00Z', '2026-01-20T09:00:00Z'),

    ('c3d4e5f6-a1b2-3456-7890-abcdef123456', 'bobsmith', 'Bob Smith',
     'Bob', 'Smith',
     'Music producer and coffee addict.',
     '1992-11-08', 'male',
     NULL, NULL,
     'personal', 'Music Producer', NULL, 'Austin, TX',
     0,
     '2026-01-15T10:02:00Z', '2026-01-15T10:02:00Z')
ON CONFLICT DO NOTHING;


-- =====================
-- profile.user_links
-- =====================
INSERT INTO profile.user_links (user_id, platform, url, display_label, sort_order)
VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'github',   'https://github.com/johndoe',    'GitHub',    0),
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'twitter',  'https://x.com/johndoe',         '@johndoe',  1),
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'linkedin', 'https://linkedin.com/in/johndoe','LinkedIn',  2),
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'dribbble', 'https://dribbble.com/janedoe',   'Dribbble',  0),
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'website',  'https://janedoe.dev',            'Portfolio',  1)
ON CONFLICT DO NOTHING;


-- =====================
-- profile.user_about
-- =====================
INSERT INTO profile.user_about (user_id, section, item_id, data, visibility, sort_order, created_at, updated_at)
VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'work',
     '10000000-0000-4000-b000-000000000001',
     '{"company":"Acme Corp","title":"Senior Software Engineer","start_year":2022,"end_year":null,"is_current":true}',
     'public', 0, '2026-01-16T10:00:00Z', '2026-01-16T10:00:00Z'),

    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'work',
     '10000000-0000-4000-b000-000000000002',
     '{"company":"StartupXYZ","title":"Full-Stack Developer","start_year":2019,"end_year":2022,"is_current":false}',
     'public', 1, '2026-01-16T10:00:00Z', '2026-01-16T10:00:00Z'),

    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'education',
     '10000000-0000-4000-b000-000000000003',
     '{"school":"UC Berkeley","degree":"B.S. Computer Science","start_year":2013,"end_year":2017}',
     'public', 0, '2026-01-16T10:00:00Z', '2026-01-16T10:00:00Z'),

    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'hobbies',
     '10000000-0000-4000-b000-000000000004',
     '{"name":"Open Source","description":"Contributing to OSS projects on weekends"}',
     'public', 0, '2026-01-16T10:00:00Z', '2026-01-16T10:00:00Z'),

    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'hobbies',
     '10000000-0000-4000-b000-000000000005',
     '{"name":"Photography","description":"Street and landscape photography"}',
     'public', 1, '2026-01-16T10:00:00Z', '2026-01-16T10:00:00Z'),

    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'hobbies',
     '10000000-0000-4000-b000-000000000006',
     '{"name":"Running","description":"Half-marathon training"}',
     'followers', 2, '2026-01-16T10:00:00Z', '2026-01-16T10:00:00Z'),

    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'contact',
     '10000000-0000-4000-b000-000000000007',
     '{"type":"email","value":"john@johndoe.dev"}',
     'public', 0, '2026-01-16T10:00:00Z', '2026-01-16T10:00:00Z'),

    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'contact',
     '10000000-0000-4000-b000-000000000008',
     '{"type":"phone","value":"+1 (555) 123-4567"}',
     'friends', 1, '2026-01-16T10:00:00Z', '2026-01-16T10:00:00Z'),

    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'life_events',
     '10000000-0000-4000-b000-000000000009',
     '{"title":"Moved to San Francisco","description":"Started a new chapter on the West Coast","date":"2022-08-01"}',
     'public', 0, '2026-01-16T10:00:00Z', '2026-01-16T10:00:00Z'),

    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'family',
     '10000000-0000-4000-b000-00000000000a',
     '{"relation":"sibling","name":"Jane Doe","user_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890"}',
     'friends', 0, '2026-01-16T10:00:00Z', '2026-01-16T10:00:00Z')
ON CONFLICT DO NOTHING;


-- =====================
-- follows
-- =====================
INSERT INTO follows (follower_id, followee_id, created_at)
VALUES
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892', '2026-01-16T12:00:00Z'),
    ('c3d4e5f6-a1b2-3456-7890-abcdef123456', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892', '2026-01-17T08:00:00Z'),
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'a1b2c3d4-e5f6-7890-abcd-ef1234567890', '2026-01-16T14:00:00Z'),
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 'c3d4e5f6-a1b2-3456-7890-abcdef123456', '2026-01-18T09:00:00Z')
ON CONFLICT DO NOTHING;


-- =====================
-- friends
-- =====================
INSERT INTO friends (user_a, user_b, created_at)
VALUES
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892', '2026-01-18T10:00:00Z')
ON CONFLICT DO NOTHING;


-- =====================
-- friend_requests
-- =====================
INSERT INTO friend_requests (sender_id, receiver_id, status, created_at, updated_at)
VALUES
    ('c3d4e5f6-a1b2-3456-7890-abcdef123456', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     'pending', '2026-02-01T09:00:00Z', '2026-02-01T09:00:00Z')
ON CONFLICT DO NOTHING;


-- =====================
-- counts
-- =====================
INSERT INTO counts (user_id, follower_count, following_count, friend_count, updated_at)
VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', 2, 2, 1, '2026-02-01T12:00:00Z'),
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 1, 1, 1, '2026-02-01T12:00:00Z'),
    ('c3d4e5f6-a1b2-3456-7890-abcdef123456', 1, 1, 0, '2026-02-01T12:00:00Z')
ON CONFLICT (user_id) DO UPDATE SET
    follower_count = EXCLUDED.follower_count,
    following_count = EXCLUDED.following_count,
    friend_count = EXCLUDED.friend_count,
    updated_at = EXCLUDED.updated_at;


-- =====================
-- media
-- =====================
INSERT INTO media (id, owner_user_id, kind, mime, size_bytes, bucket, object_key, status, created_at, updated_at)
VALUES
    ('00000000-0000-4000-a000-000000000001', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     'avatar', 'image/jpeg', 52400, 'uploads', 'avatars/b2e06bd7/avatar.jpg', 'ready',
     '2026-01-15T10:05:00Z', '2026-01-15T10:05:00Z'),
    ('00000000-0000-4000-a000-000000000002', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     'cover', 'image/jpeg', 204800, 'uploads', 'covers/b2e06bd7/cover.jpg', 'ready',
     '2026-01-15T10:06:00Z', '2026-01-15T10:06:00Z'),
    ('00000000-0000-4000-a000-000000000003', 'a1b2c3d4-e5f6-7890-abcd-ef1234567890',
     'avatar', 'image/jpeg', 48000, 'uploads', 'avatars/a1b2c3d4/avatar.jpg', 'ready',
     '2026-01-15T10:07:00Z', '2026-01-15T10:07:00Z'),
    ('20000000-0000-4000-a000-000000000001', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     'image', 'image/jpeg', 310000, 'uploads', 'posts/b2e06bd7/photo1.jpg', 'ready',
     '2026-01-20T10:00:00Z', '2026-01-20T10:00:00Z'),
    ('20000000-0000-4000-a000-000000000002', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     'image', 'image/jpeg', 280000, 'uploads', 'posts/b2e06bd7/photo2.jpg', 'ready',
     '2026-01-22T10:00:00Z', '2026-01-22T10:00:00Z'),
    ('20000000-0000-4000-a000-000000000003', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     'video', 'video/mp4', 15000000, 'uploads', 'posts/b2e06bd7/short1.mp4', 'ready',
     '2026-01-25T10:00:00Z', '2026-01-25T10:00:00Z'),
    ('20000000-0000-4000-a000-000000000004', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     'video', 'video/mp4', 85000000, 'uploads', 'posts/b2e06bd7/video1.mp4', 'ready',
     '2026-02-01T10:00:00Z', '2026-02-01T10:00:00Z')
ON CONFLICT DO NOTHING;


-- =====================
-- posts
-- =====================
INSERT INTO posts (id, author_id, text, visibility, content_type, is_pinned, created_at, updated_at)
VALUES
    ('30000000-0000-4000-a000-000000000001', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     'Just shipped a major refactor of our API layer. Clean architecture really pays off when you need to swap out a data store. Loving Go more every day!',
     'public', 'post', TRUE,
     '2026-01-20T09:30:00Z', '2026-01-20T09:30:00Z'),

    ('30000000-0000-4000-a000-000000000002', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     'Golden Gate Bridge at sunset. The fog was rolling in perfectly.',
     'public', 'photo', FALSE,
     '2026-01-22T18:45:00Z', '2026-01-22T18:45:00Z'),

    ('30000000-0000-4000-a000-000000000003', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     'Quick tip: use cursor-based pagination instead of offset-based. Your DB will thank you at scale.',
     'public', 'post', FALSE,
     '2026-01-24T11:00:00Z', '2026-01-24T11:00:00Z'),

    ('30000000-0000-4000-a000-000000000004', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     'Making espresso art is basically the same as CSS — it looks easy until you try it yourself.',
     'public', 'short', FALSE,
     '2026-01-25T08:00:00Z', '2026-01-25T08:00:00Z'),

    ('30000000-0000-4000-a000-000000000005', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     'Deep dive into our microservices architecture — how we handle 10k requests/sec with 14 services.',
     'public', 'video', FALSE,
     '2026-02-01T14:00:00Z', '2026-02-01T14:00:00Z'),

    ('30000000-0000-4000-a000-000000000006', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     'Weekend hike at Muir Woods. Nature is the best debugger — go outside and solutions come to you.',
     'public', 'photo', FALSE,
     '2026-02-05T16:30:00Z', '2026-02-05T16:30:00Z'),

    ('30000000-0000-4000-a000-000000000007', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     'Thinking about the future of social platforms. Decentralization, privacy-first, creator-owned. We are building it.',
     'public', 'post', FALSE,
     '2026-02-10T10:00:00Z', '2026-02-10T10:00:00Z'),

    ('30000000-0000-4000-a000-000000000008', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     'Late night coding session vibes. Lo-fi beats, dark mode, and a fresh pot of coffee.',
     'followers', 'post', FALSE,
     '2026-02-12T23:45:00Z', '2026-02-12T23:45:00Z'),

    ('30000000-0000-4000-a000-000000000010', 'a1b2c3d4-e5f6-7890-abcd-ef1234567890',
     'New design system is coming together. Consistent spacing + color tokens = happy developers.',
     'public', 'post', FALSE,
     '2026-01-21T14:00:00Z', '2026-01-21T14:00:00Z'),

    ('30000000-0000-4000-a000-000000000011', 'a1b2c3d4-e5f6-7890-abcd-ef1234567890',
     'NYC streets in the rain. There is something magical about neon reflections on wet pavement.',
     'public', 'photo', TRUE,
     '2026-01-28T19:00:00Z', '2026-01-28T19:00:00Z'),

    ('30000000-0000-4000-a000-000000000020', 'c3d4e5f6-a1b2-3456-7890-abcdef123456',
     'New beat dropped! Check out my latest track — link in bio.',
     'public', 'post', FALSE,
     '2026-02-03T20:00:00Z', '2026-02-03T20:00:00Z')
ON CONFLICT DO NOTHING;


-- =====================
-- post_media
-- =====================
INSERT INTO post_media (post_id, media_id, kind)
VALUES
    ('30000000-0000-4000-a000-000000000002', '20000000-0000-4000-a000-000000000001', 'image'),
    ('30000000-0000-4000-a000-000000000004', '20000000-0000-4000-a000-000000000003', 'video'),
    ('30000000-0000-4000-a000-000000000005', '20000000-0000-4000-a000-000000000004', 'video'),
    ('30000000-0000-4000-a000-000000000006', '20000000-0000-4000-a000-000000000002', 'image')
ON CONFLICT DO NOTHING;


-- =====================
-- chat.conversations
-- =====================
INSERT INTO chat.conversations (id, type, created_at, updated_at)
VALUES
    ('40000000-0000-4000-a000-000000000001', 'direct', '2026-01-20T15:00:00Z', '2026-02-10T09:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO chat.conversation_members (conversation_id, user_id, joined_at)
VALUES
    ('40000000-0000-4000-a000-000000000001', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892', '2026-01-20T15:00:00Z'),
    ('40000000-0000-4000-a000-000000000001', 'a1b2c3d4-e5f6-7890-abcd-ef1234567890', '2026-01-20T15:00:00Z')
ON CONFLICT DO NOTHING;

INSERT INTO chat.direct_conversation_keys (user_a, user_b, conversation_id)
VALUES
    ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     '40000000-0000-4000-a000-000000000001')
ON CONFLICT DO NOTHING;


-- =====================
-- celeb_authors
-- =====================
INSERT INTO celeb_authors (author_id, is_celeb, updated_at)
VALUES
    ('b2e06bd7-fa13-4f05-94cc-8973bcafe892', TRUE, '2026-02-01T12:00:00Z')
ON CONFLICT DO NOTHING;


-- =====================
-- analytics.events_raw
-- =====================
INSERT INTO analytics.events_raw (id, user_id, session_id, type, payload, ts)
VALUES
    ('50000000-0000-4000-a000-000000000001',
     'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     '60000000-0000-4000-a000-000000000001',
     'page_view', '{"path":"/","platform":"postboek"}',
     '2026-02-15T10:00:00Z'),
    ('50000000-0000-4000-a000-000000000002',
     'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     '60000000-0000-4000-a000-000000000001',
     'profile_view', '{"viewed_user":"a1b2c3d4-e5f6-7890-abcd-ef1234567890","platform":"postboek"}',
     '2026-02-15T10:01:00Z'),
    ('50000000-0000-4000-a000-000000000003',
     'b2e06bd7-fa13-4f05-94cc-8973bcafe892',
     '60000000-0000-4000-a000-000000000001',
     'post_create', '{"post_id":"30000000-0000-4000-a000-000000000007","content_type":"post"}',
     '2026-02-10T10:00:00Z')
ON CONFLICT DO NOTHING;
