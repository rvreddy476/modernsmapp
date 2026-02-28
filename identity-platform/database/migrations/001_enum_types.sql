-- Migration 001: Create all ENUM types for identity_db
-- Run against: identity_db

\connect identity_db;

-- Auth enums
CREATE TYPE account_type AS ENUM ('personal', 'creator', 'business');
CREATE TYPE account_status AS ENUM ('active', 'suspended', 'deleted', 'pending_deletion');
CREATE TYPE age_verification AS ENUM ('unverified', 'under_16', 'minor', 'adult');

-- Profile enums
CREATE TYPE profile_category AS ENUM ('personal', 'creator', 'business');
CREATE TYPE verification_level AS ENUM ('email', 'phone', 'id', 'org');
CREATE TYPE intro_media_type AS ENUM ('audio', 'video');
CREATE TYPE profile_section_type AS ENUM ('basic_info', 'contact', 'location', 'life_entry', 'interests', 'services');
CREATE TYPE visibility_level AS ENUM ('public', 'followers', 'friends', 'only_me');

-- Social enums
CREATE TYPE follow_status AS ENUM ('active', 'pending');
CREATE TYPE friendship_status AS ENUM ('pending', 'accepted', 'rejected', 'blocked');
