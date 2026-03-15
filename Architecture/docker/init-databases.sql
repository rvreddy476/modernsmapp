-- Create additional databases needed by services
-- (Postgres auto-creates 'app' via POSTGRES_DB env var)

SELECT 'CREATE DATABASE identity_db'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'identity_db')\gexec

SELECT 'CREATE DATABASE chat_db'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'chat_db')\gexec

SELECT 'CREATE DATABASE commerce_db'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'commerce_db')\gexec

SELECT 'CREATE DATABASE feed_db'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'feed_db')\gexec

SELECT 'CREATE DATABASE call_db'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'call_db')\gexec
