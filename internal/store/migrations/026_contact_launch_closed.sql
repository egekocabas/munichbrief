-- Require an explicit admin decision before receiving correspondence or sending
-- notifications. Applied once, including databases used to preview this feature.
-- Subsequent restarts preserve the administrator's saved controls.
UPDATE contact_settings SET form_enabled=0, notifications_enabled=0 WHERE id=1;
UPDATE contact_messages SET notification_state='cancelled', notification_code='test_disabled'
 WHERE is_test=1 AND notification_state IN ('pending','retry');
