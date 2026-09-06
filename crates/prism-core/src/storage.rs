//! Private application files. Callers must supply an application-owned directory.
use std::fs::{self, File, OpenOptions};
use std::io::{self, Write};
use std::path::Path;

fn reject_link(path: &Path) -> io::Result<()> {
    let metadata = fs::symlink_metadata(path)?;
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        if metadata.file_attributes() & 0x400 != 0 {
            return Err(io::Error::new(
                io::ErrorKind::PermissionDenied,
                "application storage cannot be a reparse point",
            ));
        }
    }
    if metadata.file_type().is_symlink() || !(metadata.is_file() || metadata.is_dir()) {
        return Err(io::Error::new(
            io::ErrorKind::PermissionDenied,
            "application storage must be a regular file or directory",
        ));
    }
    Ok(())
}

/// Repair permissions on existing files as well as newly created ones.
pub(crate) fn protect(path: &Path) -> io::Result<()> {
    reject_link(path)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt};
        let file = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_NOFOLLOW)
            .open(path)?;
        let metadata = file.metadata()?;
        if metadata.uid() != unsafe { libc::geteuid() } {
            return Err(io::Error::new(
                io::ErrorKind::PermissionDenied,
                "application storage belongs to another user",
            ));
        }
        file.set_permissions(fs::Permissions::from_mode(if metadata.is_dir() {
            0o700
        } else {
            0o600
        }))?;
    }
    #[cfg(windows)]
    windows_private_acl(path)?;
    Ok(())
}

pub(crate) fn private_dir(path: &Path) -> io::Result<()> {
    if path.as_os_str().is_empty() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "an application storage directory is required",
        ));
    }
    fs::create_dir_all(path)?;
    protect(path)
}

pub(crate) fn prepare(path: &Path) -> io::Result<()> {
    private_dir(path.parent().ok_or_else(|| {
        io::Error::new(
            io::ErrorKind::InvalidInput,
            "storage path needs a parent directory",
        )
    })?)?;
    match fs::symlink_metadata(path) {
        Ok(_) => protect(path),
        Err(err) if err.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(err) => Err(err),
    }
}

pub(crate) fn read(path: &Path) -> io::Result<File> {
    prepare(path)?;
    let mut options = OpenOptions::new();
    options.read(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.custom_flags(libc::O_NOFOLLOW);
    }
    options.open(path)
}

pub(crate) fn append(path: &Path) -> io::Result<File> {
    prepare(path)?;
    let mut options = OpenOptions::new();
    options.create(true).append(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600).custom_flags(libc::O_NOFOLLOW);
    }
    let file = options.open(path)?;
    protect(path)?;
    Ok(file)
}

/// Same-directory replacement prevents a failed write from truncating the live config.
pub(crate) fn atomic_write(path: &Path, bytes: &[u8]) -> io::Result<()> {
    prepare(path)?;
    let parent = path.parent().expect("prepared path");
    let mut temp = tempfile::NamedTempFile::new_in(parent)?;
    protect(temp.path())?;
    temp.write_all(bytes)?;
    temp.as_file().sync_all()?;
    temp.persist(path).map_err(|err| err.error)?;
    #[cfg(unix)]
    File::open(parent)?.sync_all()?;
    Ok(())
}

/// Replace a user-owned client config without changing permissions on its existing directory.
/// The replacement itself is private on Unix and Windows, just like Prism's own secrets.
pub fn write_client_config(path: &Path, bytes: &[u8]) -> io::Result<()> {
    let parent = path
        .parent()
        .ok_or_else(|| io::Error::other("config needs a directory"))?;
    if !parent.exists() {
        private_dir(parent)?;
    }
    reject_link(parent)?;
    if fs::symlink_metadata(path).is_ok() {
        reject_link(path)?;
    }
    let mut temp = tempfile::NamedTempFile::new_in(parent)?;
    protect(temp.path())?;
    temp.write_all(bytes)?;
    temp.as_file().sync_all()?;
    temp.persist(path).map_err(|err| err.error)?;
    #[cfg(unix)]
    File::open(parent)?.sync_all()?;
    Ok(())
}

#[cfg(windows)]
fn windows_private_acl(path: &Path) -> io::Result<()> {
    use std::os::windows::ffi::OsStrExt;
    use std::ptr::{null, null_mut};
    use windows_sys::Win32::Foundation::{CloseHandle, LocalFree};
    use windows_sys::Win32::Security::Authorization::{
        ConvertSidToStringSidW, ConvertStringSecurityDescriptorToSecurityDescriptorW,
        SetNamedSecurityInfoW, SE_FILE_OBJECT,
    };
    use windows_sys::Win32::Security::{
        GetSecurityDescriptorDacl, GetTokenInformation, TokenUser, DACL_SECURITY_INFORMATION,
        PROTECTED_DACL_SECURITY_INFORMATION, TOKEN_QUERY, TOKEN_USER,
    };
    use windows_sys::Win32::System::Threading::{GetCurrentProcess, OpenProcessToken};

    // All buffers and handles outlive the Win32 calls that borrow them. The DACL is
    // protected from inheritance and grants access only to this user and SYSTEM.
    unsafe {
        let mut token = null_mut();
        if OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut token) == 0 {
            return Err(io::Error::last_os_error());
        }
        let result = (|| {
            let mut len = 0;
            GetTokenInformation(token, TokenUser, null_mut(), 0, &mut len);
            let mut buffer = vec![0usize; (len as usize).div_ceil(std::mem::size_of::<usize>())];
            if GetTokenInformation(token, TokenUser, buffer.as_mut_ptr().cast(), len, &mut len) == 0
            {
                return Err(io::Error::last_os_error());
            }
            let user = &*buffer.as_ptr().cast::<TOKEN_USER>();
            let mut sid_text = null_mut();
            if ConvertSidToStringSidW(user.User.Sid, &mut sid_text) == 0 {
                return Err(io::Error::last_os_error());
            }
            let mut sid_len = 0;
            while *sid_text.add(sid_len) != 0 {
                sid_len += 1;
            }
            let sid = String::from_utf16_lossy(std::slice::from_raw_parts(sid_text, sid_len));
            LocalFree(sid_text.cast());
            let sddl: Vec<u16> = format!("D:P(A;OICI;FA;;;{sid})(A;OICI;FA;;;SY)")
                .encode_utf16()
                .chain(Some(0))
                .collect();
            let mut descriptor = null_mut();
            if ConvertStringSecurityDescriptorToSecurityDescriptorW(
                sddl.as_ptr(),
                1,
                &mut descriptor,
                null_mut(),
            ) == 0
            {
                return Err(io::Error::last_os_error());
            }
            let applied = (|| {
                let mut present = 0;
                let mut defaulted = 0;
                let mut dacl = null_mut();
                if GetSecurityDescriptorDacl(descriptor, &mut present, &mut dacl, &mut defaulted)
                    == 0
                    || present == 0
                {
                    return Err(io::Error::last_os_error());
                }
                let name: Vec<u16> = path.as_os_str().encode_wide().chain(Some(0)).collect();
                let status = SetNamedSecurityInfoW(
                    name.as_ptr(),
                    SE_FILE_OBJECT,
                    DACL_SECURITY_INFORMATION | PROTECTED_DACL_SECURITY_INFORMATION,
                    null_mut(),
                    null_mut(),
                    dacl,
                    null(),
                );
                if status == 0 {
                    Ok(())
                } else {
                    Err(io::Error::from_raw_os_error(status as i32))
                }
            })();
            LocalFree(descriptor);
            applied
        })();
        CloseHandle(token);
        result
    }
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;

    #[cfg(unix)]
    #[test]
    fn repairs_existing_permissions_and_rejects_symlinks() {
        use std::os::unix::fs::{symlink, PermissionsExt};
        let root = tempfile::tempdir().unwrap();
        let dir = root.path().join("app");
        fs::create_dir(&dir).unwrap();
        let path = dir.join("prism.json");
        fs::write(&path, b"old").unwrap();
        fs::set_permissions(&dir, fs::Permissions::from_mode(0o777)).unwrap();
        fs::set_permissions(&path, fs::Permissions::from_mode(0o666)).unwrap();
        atomic_write(&path, b"new").unwrap();
        assert_eq!(
            fs::metadata(&dir).unwrap().permissions().mode() & 0o777,
            0o700
        );
        assert_eq!(
            fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
        assert_eq!(fs::read(&path).unwrap(), b"new");
        let link = dir.join("link");
        symlink(&path, &link).unwrap();
        assert!(atomic_write(&link, b"bad").is_err());
        assert_eq!(fs::read(&path).unwrap(), b"new");
    }
}
