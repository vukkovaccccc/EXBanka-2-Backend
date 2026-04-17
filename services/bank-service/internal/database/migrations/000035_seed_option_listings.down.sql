-- Rollback: brisanje kreiranog OPTION listinga (worker će ih ponovo kreirati pri sledećem pokretanju)
DELETE FROM core_banking.listing WHERE listing_type = 'OPTION';
