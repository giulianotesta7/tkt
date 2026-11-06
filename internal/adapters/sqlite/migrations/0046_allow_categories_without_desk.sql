-- Expand category ownership without rewriting any legacy association (#491).
-- The nullable column and its foreign key stay until the later contract slice.
-- Reinstating these triggers is not a lossless rollback once deskless rows exist:
-- their ownership would require an explicit policy, never an inferred General.
DROP TRIGGER categories_desk_required_insert;
DROP TRIGGER categories_desk_required_update;
