import axios from 'axios';

const fetchAnalyticsData = async (query: string) => {
    const params = new URLSearchParams();
    params.append('query', query);

    try {
        const response = await axios.get('http://localhost:9091/api/v1/queries', { params });
        return response.data;
    } catch (error) {
        throw error;
    }
};

export default fetchAnalyticsData;