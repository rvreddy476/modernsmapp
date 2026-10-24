-- 045: books-only sellers — PAN and proof of address, no GSTIN.
--
-- Founder decision, 24 Oct 2026: MSeller accepts a seller who sells ONLY
-- books with a PAN and a proof of address, and no GSTIN (the "Only Books"
-- path Flipkart offers). Every other category still needs a GSTIN.
--
-- Why books: printed books are GST-exempt, and a person whose supplies are
-- wholly exempt is not liable to register (CGST Act s.23(1)(a)), which is
-- what lets them sell through an e-commerce operator without a GSTIN. The
-- service therefore holds a books-only shop to book categories AND to a
-- zero-rated tax class — an unregistered seller must not collect GST at all.
--
-- Expand-only. Every change below widens what the schema accepts or adds a
-- column with a default an older image never writes:
--
--   sellers.sell_scope             new, NOT NULL DEFAULT 'all_categories', so
--                                  every existing shop keeps selling exactly
--                                  what it sold yesterday
--   seller_documents.back_media_id new, nullable: the back of a two-sided
--                                  proof (voter ID, passport)
--   seller_documents.document_type CHECK widened by four proof-of-address
--                                  kinds; a strict superset of migration 001
--   product_categories.is_book_category
--                                  new, DEFAULT FALSE; inherited down the
--                                  tree by the service, so flagging one node
--                                  covers its children
--
-- The PAN itself needs no column: migration 035 already gave sellers
-- pan_enc / pan_masked / pan_hash / pan_key_version, sealed under the kyc
-- scope. The business address is a `seller_addresses` row of address_type
-- 'business' (setup.sql already permits it), sealed like every other seller
-- address.

ALTER TABLE sellers
    ADD COLUMN IF NOT EXISTS sell_scope TEXT NOT NULL DEFAULT 'all_categories'
        CHECK (sell_scope IN ('all_categories', 'books_only'));

COMMENT ON COLUMN sellers.sell_scope IS
    'What this shop may list. all_categories needs a GSTIN; books_only needs a PAN and a proof of '
    'address and may list only in book categories, on a zero-rated tax class. Chosen at onboarding; '
    'books_only -> all_categories only by adding a GSTIN (POST /seller/sell-scope/upgrade).';

ALTER TABLE seller_documents
    ADD COLUMN IF NOT EXISTS back_media_id UUID;

COMMENT ON COLUMN seller_documents.back_media_id IS
    'The back of a two-sided document (voter_id, passport), uploaded through media-service and '
    'verified as the seller''s own, like media_id. NULL for single-sided documents.';

-- The document vocabulary, widened. The constraint migration 001 created
-- inline carries the default name; a database whose constraint was renamed
-- is found by its definition instead.
DO $$
DECLARE
    c TEXT;
BEGIN
    FOR c IN
        SELECT con.conname
          FROM pg_constraint con
          JOIN pg_class rel ON rel.oid = con.conrelid
         WHERE rel.relname = 'seller_documents'
           AND con.contype = 'c'
           AND pg_get_constraintdef(con.oid) ILIKE '%document_type%'
           AND pg_get_constraintdef(con.oid) NOT ILIKE '%voter_id%'
    LOOP
        EXECUTE format('ALTER TABLE seller_documents DROP CONSTRAINT %I', c); -- expand-only: re-added immediately below as a strict superset (four new kinds, none removed)
    END LOOP;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint con
          JOIN pg_class rel ON rel.oid = con.conrelid
         WHERE rel.relname = 'seller_documents'
           AND con.conname = 'seller_documents_document_type_check'
    ) THEN
        ALTER TABLE seller_documents ADD CONSTRAINT seller_documents_document_type_check -- expand-only: strict superset of migration 001's list; every existing row already satisfies it
            CHECK (document_type IN ('gst_certificate','pan_card','aadhaar','passport',
                                     'business_registration','address_proof','cancelled_cheque','other',
                                     'voter_id','electricity_bill','telephone_bill','bank_statement'));
    END IF;
END $$;

ALTER TABLE product_categories
    ADD COLUMN IF NOT EXISTS is_book_category BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN product_categories.is_book_category IS
    'TRUE marks a category (and, through the service, every category beneath it) as books: the only '
    'place a books_only seller may list. Deliberately NOT set on "Books & Stationery" itself — '
    'stationery is taxable, and a stationery child added there later must not inherit the books path.';

-- Textbooks (migration 025) is the one book leaf seeded today. Keyed on the
-- slug, which 023 made the convergence key.
UPDATE product_categories SET is_book_category = TRUE, updated_at = NOW()
 WHERE slug = 'books-textbooks' AND is_book_category = FALSE;
