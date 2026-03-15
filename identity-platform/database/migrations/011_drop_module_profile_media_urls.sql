-- 011_drop_module_profile_media_urls.sql
-- V4 Profile Media Spec: Remove direct media URL columns from module_profiles.
-- All media is now managed through owner_media_slots in the media service.
ALTER TABLE profile.module_profiles DROP COLUMN IF EXISTS avatar_override_url;
ALTER TABLE profile.module_profiles DROP COLUMN IF EXISTS banner_url;
ALTER TABLE profile.module_profiles DROP COLUMN IF EXISTS watermark_url;
