//! One writer owns a profile for its entire lifetime, including offline imports.
use std::{
    fs::{File, OpenOptions},
    path::Path,
};

use crate::{Error, Result};

pub(crate) struct ProfileLock {
    _file: File,
}

impl ProfileLock {
    pub(crate) fn acquire(config: &Path) -> Result<Self> {
        crate::storage::prepare(config)?;
        let mut name = config
            .file_name()
            .ok_or_else(|| Error::Invalid("a config file is required".into()))?
            .to_os_string();
        name.push(".lock");
        // Never unlink this file: another process may already be waiting on its inode.
        let path = config.with_file_name(name);
        crate::storage::prepare(&path)?;
        let mut options = OpenOptions::new();
        options.create(true).truncate(false).read(true).write(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600).custom_flags(libc::O_NOFOLLOW);
        }
        let file = options.open(&path)?;
        crate::storage::protect(&path)?;
        file.try_lock().map_err(|error| match error {
            std::fs::TryLockError::WouldBlock => Error::Invalid(
                "this profile is in use; quit Prism before applying configuration".into(),
            ),
            std::fs::TryLockError::Error(_) => {
                Error::Invalid("could not lock this profile; configuration was not changed".into())
            }
        })?;
        Ok(Self { _file: file })
    }
}
