# prom-analytics-proxy

[![Build](https://github.com/MichaHoffmann/prom-analytics-proxy/actions/workflows/ci.yaml/badge.svg)](https://github.com/MichaHoffmann/prom-analytics-proxy/actions/workflows/ci.yaml)

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Project Structure](#project-structure)

## Overview

`prom-analytics-proxy` is a lightweight proxy application designed to sit between your Prometheus server and its clients. It provides valuable insights by collecting detailed analytics on PromQL queries, helping you understand query performance, resource usage, and overall system behavior. This can significantly improve observability for Prometheus users, providing actionable data to optimize query execution and infrastructure.

## Features

- **Query Analytics**: Collects detailed statistics on PromQL queries, including query execution times, resource consumption, and the number of series touched.
- **Data Storage**: Supports storing the collected analytics data in either ClickHouse or PostgreSQL, giving flexibility based on your database preferences.
- **User Interface**: Provides an intuitive web-based UI to explore and visualize the analytics data, helping engineers make data-driven decisions on query optimizations.
- **Easy Integration**: Seamlessly integrates with existing Prometheus setups with minimal configuration.

## Project Structure

The project is organized into the following core components:

- **`prom-analytics-proxy`**: A Go-based backend application responsible for acting as a proxy between Prometheus and clients. It captures and processes analytics from PromQL queries, offering insights into query performance metrics such as execution time, resource usage, and efficiency.

- **`prom-analytics-proxy-ui`**: A React-based user interface located in the `ui` directory. This component provides a visual platform to explore the analytics data collected by the proxy, making it easier to analyze and identify trends in PromQL queries.

Both components are designed to work together, with `prom-analytics-proxy` handling data collection and backend logic, while `prom-analytics-proxy-ui` provides a frontend interface for exploring query insights.
