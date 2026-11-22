use super::process::{ServerEvent, ServerProcess};
use crate::stremio_app::constants::{DEV_ENDPOINT, STREMIO_SERVER_DEV_MODE};
use flume::{Receiver, Sender};
use native_windows_gui::{self as nwg, PartialUi};
use std::{
    env, fs, os::windows::process::CommandExt, path::PathBuf, process::Command, time::Duration,
};
use winapi::um::winbase::{CREATE_BREAKAWAY_FROM_JOB, CREATE_NO_WINDOW};

const STARTUP_TIMEOUT: Duration = Duration::from_secs(30);
/// The port the web UI and the server's own clients expect (as server.js).
const HTTP_PORT: &str = "11470";
/// A fixed peer port, so it can be forwarded on the router.
const BT_LISTEN_PORT: &str = "49881";

/// The streaming server is the bundled Rivulet Engine. It lives for this app
/// (RIVULET_OWNER_PID): when the app exits, it exits too, after finishing
/// downloads if any are left. A server still finishing them when the app
/// starts again is adopted instead of starting a second one.
#[derive(Default)]
pub struct StremioServer {
    pub notice: nwg::Notice,
    process: Option<ServerProcess>,
    adopted: Option<(Sender<ServerEvent>, Receiver<ServerEvent>)>,
}

impl StremioServer {
    pub fn development(&self) -> bool {
        self.process.is_none()
    }

    pub fn start(&self) {
        let (Some(process), Some((adopted, _))) = (&self.process, &self.adopted) else {
            return;
        };
        if adopt() {
            let endpoint = format!("{DEV_ENDPOINT}/");
            if adopted.send(ServerEvent::Ready(endpoint)).is_ok() {
                self.notice.sender().notice();
            }
        } else {
            process.start();
        }
    }

    pub fn events(&self) -> impl Iterator<Item = ServerEvent> + '_ {
        self.adopted
            .iter()
            .flat_map(|(_, events)| events.try_iter())
            .chain(self.process.iter().flat_map(ServerProcess::events))
    }
}

/// Takes over a server left running by a previous run (finishing downloads):
/// it becomes this app's, and keeps running while the app does.
fn adopt() -> bool {
    let Ok(client) = reqwest::blocking::Client::builder()
        .timeout(Duration::from_secs(2))
        .build()
    else {
        return false;
    };
    client
        .post(format!("{DEV_ENDPOINT}/downloads/owner"))
        .json(&serde_json::json!({ "pid": std::process::id() }))
        .send()
        .is_ok_and(|response| response.status().is_success())
}

fn data_dir() -> Result<PathBuf, String> {
    let dir = dirs::data_local_dir()
        .ok_or("Cannot locate the local app data folder")?
        .join("rivulet-engine");
    fs::create_dir_all(&dir)
        .map_err(|error| format!("Cannot create {}: {error}", dir.display()))?;
    Ok(dir)
}

fn server_command() -> Result<Command, String> {
    super::job::protect_shell()
        .map_err(|error| format!("Cannot supervise Stremio child processes: {error}"))?;
    let mut dir = env::current_exe()
        .map_err(|error| format!("Cannot locate the Stremio installation: {error}"))?;
    dir.pop();
    let data = data_dir()?;
    // ffmpeg and ffprobe are installed next to the app.
    let mut path = dir.clone().into_os_string();
    if let Some(rest) = env::var_os("PATH") {
        path.push(";");
        path.push(rest);
    }
    let mut command = Command::new(dir.join("rivulet-engine.exe"));
    command
        .env("BIND_ADDRESS", "127.0.0.1") // the API is unauthenticated: loopback only
        .env("HTTP_PORT", HTTP_PORT)
        .env("HTTPS_PORT", "0")
        .env("BT_LISTEN_PORT", BT_LISTEN_PORT)
        .env("APP_PATH", &data)
        .env("RIVULET_WEBUI_DIR", dir.join("web"))
        .env("RIVULET_OWNER_PID", std::process::id().to_string())
        .env("RIVULET_LOG_FILE", data.join("rivulet.log"))
        // No download throttles: the official speed cap and soft limit stopped
        // 4K streams at 11-17 peers (streaming-research findings/42).
        .env("RIVULET_SPEED_LIMITS", "0")
        // The development web UI (`--development --webui-url=...`).
        .env("RIVULET_ALLOWED_ORIGINS", "http://127.0.0.1:8080")
        .env("PATH", path)
        // Outside the shell's job, so it can finish downloads after the app exits.
        .creation_flags(CREATE_NO_WINDOW | CREATE_BREAKAWAY_FROM_JOB);
    Ok(command)
}

impl PartialUi for StremioServer {
    fn build_partial<W: Into<nwg::ControlHandle>>(
        data: &mut Self,
        parent: Option<W>,
    ) -> Result<(), nwg::NwgError> {
        let parent = parent.expect("No parent window").into();
        nwg::Notice::builder()
            .parent(parent)
            .build(&mut data.notice)?;

        if env::var(STREMIO_SERVER_DEV_MODE).as_deref() != Ok("true") {
            let sender = data.notice.sender();
            data.process = Some(ServerProcess::detached(
                server_command,
                STARTUP_TIMEOUT,
                move || {
                    sender.notice();
                },
            ));
            data.adopted = Some(flume::unbounded());
        }
        Ok(())
    }
}
