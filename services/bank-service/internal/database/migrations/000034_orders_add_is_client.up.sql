-- Migration: 000034_orders_add_is_client
-- Dodaje kolonu is_client u tabelu orders kako bi engine znao da li koristiti
-- prodajni kurs menjačnice pri konverziji USD iznosa u valutu računa klijenta.

ALTER TABLE core_banking.orders
    ADD COLUMN IF NOT EXISTS is_client BOOLEAN NOT NULL DEFAULT FALSE;
