-- The existing app installation can upgrade without changing its Firebase package.
ALTER TABLE user_devices DROP CONSTRAINT IF EXISTS user_devices_app_check;
ALTER TABLE user_devices ADD CONSTRAINT user_devices_app_check
    CHECK (app IN ('momentum', 'feast_kitchen', 'feast_rider', 'mopedu_captain', 'doorstep_pro', 'partner'));
