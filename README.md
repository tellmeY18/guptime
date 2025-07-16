# Website Monitor

Website Monitor is a simple Go-based tool that periodically checks the availability and response time of specified websites. It logs this data and provides a web interface to visualize the performance over time.

## Features

*   Monitors multiple websites concurrently.
*   Records response time and HTTP status codes.
*   Stores historical data in an SQLite database.
*   Provides a real-time web dashboard to view monitor status and performance graphs.

## Project Structure

*   `guptime/main.go`: The single application file containing both the monitoring service and the web server.
*   `guptime/monitors.json`: Configuration file listing the websites to monitor.
*   `guptime/data.db`: SQLite database where monitoring data is stored.
*   `static/`: Directory for static assets like `style.css`.
*   `README.md`: This file.

## Usage

### 1. Configure Monitors

Edit the `guptime/monitors.json` file to list the websites you want to monitor.

**Example `monitors.json`:**
```json
{
  "Neatnik": "https://neatnik.net",
  "Google": {
    "url": "https://www.google.com"
  }
}
```

### 2. Run the Application

In your terminal, run the following commands:

```bash
# Download dependencies
go mod tidy

# Run the application
go run guptime/main.go
```

The application will start, and you will see log output in your terminal. It automatically:
*   Creates and sets up a `data.db` database file.
*   Starts monitoring websites in the background.
*   Launches a web server.

### 3. View the Dashboard

Open your web browser and go to `http://localhost:8080`.

## Production Deployment

For a production environment, you can compile the project into a single binary. This makes deployment simple, as you only need to manage the binary and its configuration file.

### 1. Build the Binary

From the root of the project, run the following command to build the application:

```bash
# This creates a binary named 'guptime' in the current directory
go build -o guptime .
```

You can replace `guptime` with any name you prefer for the executable file.

### 2. Prepare the Production Directory

Create a directory on your server where the application will live. For example:

```bash
mkdir /opt/guptime
```

Copy the built binary and your `monitors.json` file into this directory:

```bash
cp guptime /opt/guptime/
cp monitors.json /opt/guptime/
```

The directory should look like this:
```
/opt/guptime/
├── guptime
└── monitors.json
```

The `data.db` SQLite database file will be automatically created in this directory when the application starts for the first time.

### 3. Run in Production

Navigate to your production directory and run the binary:

```bash
cd /opt/guptime
./guptime
```

The application will now be running. For long-running production use, it is recommended to run the binary as a systemd service or use a process manager like `supervisor` to ensure it runs continuously and restarts on failure.

## Contributing

Contributions are welcome! Please feel free to submit pull requests or open issues on the project's repository.

## License

This project is licensed under the MIT License. See the LICENSE file for details.