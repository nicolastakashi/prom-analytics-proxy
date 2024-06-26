import axios from 'axios';

const fetchAnalyticsData = async (query: string) => {
    const params = new URLSearchParams();
    params.append('query', query);

    const response = await axios.get('http://localhost:9091/api/v1/analytics', { params });
    return response.data;
};

export default fetchAnalyticsData;