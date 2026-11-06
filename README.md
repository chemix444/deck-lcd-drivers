# deck-lcd-drivers

one-click installer for the official Windows drivers on the **Steam Deck LCD**.

download `DeckLCDDrivers.exe` from [Releases](../../releases/latest), run it on the Deck, and accept the admin prompt.

## what it does

1. reads Valve's [Steam Hardware - Windows Resources](https://help.steampowered.com/en/faqs/view/6121-ECCD-D643-BAA8) page and pulls the current Steam Deck LCD driver links from it, so it always installs whatever Valve lists right now. if the page can't be read, it falls back to built-in links.
2. downloads each zip from `steamdeck-packages.steamos.cloud` (Valve's own host).
3. installs them in Valve's order, the same way Valve's instructions say to:

| driver | how it's installed |
| --- | --- |
| APU (graphics + audio dependency) | runs `setup.exe` |
| Wi-Fi | runs `install.bat` |
| Bluetooth | runs `installdriver.cmd` |
| SD card reader | runs `setup.exe` |
| audio 1/2 (`cs35l41.inf`) | `pnputil /add-driver ... /install` |
| audio 2/2 (`NAU88L21.inf`) | `pnputil /add-driver ... /install` |

the APU and SD card installers open their own windows, so click through those when they appear. restart when it's done.

## notes

- **LCD only.** the OLED uses different Wi-Fi, Bluetooth and audio hardware. the program checks the BIOS product name (`Jupiter` = LCD) and asks before continuing on anything else.
- the APU download is about 700 MB.
- you need internet before Wi-Fi works, so use a USB-C Ethernet hub (or USB tethering from a phone) on a fresh Windows install.
- Valve provides these drivers as-is and doesn't support Windows on the Deck. this project isn't affiliated with Valve.

## building

```
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o DeckLCDDrivers.exe .
```
