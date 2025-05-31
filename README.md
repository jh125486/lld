# LinkedIn Learning Downloader (lld)

## Overview
`lld` is a tool designed to download course videos and transcripts from LinkedIn Learning. 
It automates the process of scraping course content, saving videos, and generating transcripts in both `.txt` and `.json` formats.
I got tired of fighting the LinkedInLearning API, and other Python programs, so I broken down and wrote this with `chromedp`.

## Features
- **SSO Login**: Supports enterprise Single Sign-On (SSO) for authentication.
- **Video Download**: Automatically downloads course videos in `.mp4` format.
- **Transcript Extraction**: Extracts and saves transcripts in `.txt` or `.json` formats.
- **Course Parsing**: Parses course structure to identify sections and videos.

## Requirements
- **Go**: Ensure Go is installed on your system.
- **Chromedp**: Used for browser automation.
- **LinkedIn Learning Account**: Required to access course content.

## Installation
1. Clone the repository:
   ```bash
   git clone https://github.com/jh125486/lld.git
   cd lld
   ```

2. Install dependencies:
   ```bash
   go mod tidy
   ```

## Usage
1. Build the tool:
   ```bash
   go build -o lld
   ```

2. Run the tool with the course URL:
   ```bash
   ./lld -courseURL="https://www.linkedin.com/learning/course-name"
   ```
3. Required flags:
    - `-course`: The URL of the LinkedIn Learning course you want to download.
    - `-sso`: The URL for enterprise Single Sign-On (SSO).

One of the following flags is also required:
    - `-transcripts`: Download transcripts.
    - `-videos`: Download videos.

4. Optional flags:
    - `-json`: Save transcripts in `.json` format.
    - `-backoff`: Set a custom backoff time for retries.
    - `-timeout`: Set a custom timeout for browser operations.

### Example command:
   ```bash
   ./lld go run . \
      -sso 'http://www.linkedin.com/checkpoint/enterprise/login/74650474?application=learning&appInstanceId=46437124&authModeId=6536630950934134784' \
      -course 'https://www.linkedin.com/learning/how-to-speak-smarter-when-put-on-the-spot/spontaneity-takes-preparation-20294708?u=74650474' \
      -transcripts \
      -json \
      -videos
   ```

## Notes
- Ensure you have access to the course content before using the tool.
- Use responsibly and adhere to LinkedIn Learning's terms of service.

## License
This project is licensed under the MIT License. See the `LICENSE` file for details.
